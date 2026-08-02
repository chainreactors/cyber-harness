package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	execpb "github.com/chainreactors/aiscan/aop/exec"
	filepb "github.com/chainreactors/aiscan/aop/file"
	ptypb "github.com/chainreactors/aiscan/aop/pty"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/tool"
	commandpb "github.com/chainreactors/aiscan/pkg/types/command"
	reloadpb "github.com/chainreactors/aiscan/pkg/types/reload"
	terminalcodec "github.com/chainreactors/aiscan/pkg/web/terminal"
	"github.com/chainreactors/utils/pty"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type webSocketEnvelopeStream struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (s *webSocketEnvelopeStream) Close() error { return s.conn.Close() }
func (s *webSocketEnvelopeStream) Recv() (*aop.Envelope, error) {
	_, data, err := s.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	envelope := new(aop.Envelope)
	if err := protobuf.Unmarshal(data, envelope); err != nil {
		return nil, fmt.Errorf("decode AOP envelope: %w", err)
	}
	return envelope, nil
}

func (s *webSocketEnvelopeStream) Send(envelope *aop.Envelope) error {
	data, err := protobuf.Marshal(envelope)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
}

func dialProtoWebSocket(ctx context.Context, cc connectionConfig) (*webSocketEnvelopeStream, error) {
	dialURL, accessKey := SplitAccessKey(cc.ServerURL)
	if cc.Token != "" {
		accessKey = cc.Token
	}
	path := cc.WSPath
	if path == "" {
		path = DefaultWSPath
	}
	var headers http.Header
	if accessKey != "" {
		headers = http.Header{"Authorization": {"Bearer " + accessKey}}
	}
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, HTTPToWS(dialURL)+path, headers)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	return &webSocketEnvelopeStream{conn: conn}, nil
}

func connectGenerated(ctx context.Context, cc connectionConfig) error {
	logger := cc.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	attempt := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		stream, err := dialProtoWebSocket(ctx, cc)
		if err == nil {
			done := make(chan struct{})
			go func() {
				select {
				case <-ctx.Done():
					_ = stream.Close()
				case <-done:
				}
			}()
			err = serveAgentConnection(ctx, cc, logger, stream)
			close(done)
			_ = stream.Close()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		delay := agent.RetryDelay(attempt)
		attempt++
		logger.Warnf("connection lost (attempt %d), retrying in %v: %v", attempt, delay, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

var envelopeSequence atomic.Uint64

func nextEnvelopeID(prefix string) string {
	return prefix + ":" + strconv.FormatInt(time.Now().UnixNano(), 36) + ":" + strconv.FormatUint(envelopeSequence.Add(1), 36)
}

func serveAgentConnection(ctx context.Context, cc connectionConfig, logger telemetry.Logger, stream aop.EnvelopeStream) error {
	if cc.Registry == nil {
		return fmt.Errorf("command registry is nil")
	}
	hello, err := BuildHello(cc.Name, cc.Registry, cc.Node, cc.Runtime)
	if err != nil {
		return err
	}
	if len(cc.Capabilities) > 0 {
		hello.Capabilities = append([]string(nil), cc.Capabilities...)
	} else if cc.Chat == nil {
		hello.Capabilities = []string{"pty", "file", "exec", "tool", "sco"}
	}
	helloEnvelope, err := aop.Wrap(nextEnvelopeID("hello"), "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentHello{AgentHello: hello}})
	if err != nil {
		return err
	}
	if err := stream.Send(helloEnvelope); err != nil {
		return err
	}
	acceptedEnvelope, err := stream.Recv()
	if err != nil {
		return err
	}
	acceptedMessage, err := aop.Unwrap(acceptedEnvelope)
	if err != nil {
		return err
	}
	coreAccepted, ok := acceptedMessage.(*aop.ProtocolMessage)
	if !ok || coreAccepted.GetAgentAccepted() == nil || acceptedEnvelope.ReplyTo != helloEnvelope.Id {
		return fmt.Errorf("expected AOP agent acceptance")
	}

	connectionCtx, cancelConnection := context.WithCancel(ctx)
	defer cancelConnection()
	sendCh := make(chan *aop.Envelope, 64)
	writeErr := make(chan error, 1)
	send := func(replyTo string, message protobuf.Message) {
		envelope, wrapErr := aop.Wrap(nextEnvelopeID("agent"), replyTo, message)
		if wrapErr != nil {
			logger.Warnf("encode AOP message: %v", wrapErr)
			return
		}
		select {
		case sendCh <- envelope:
		case <-connectionCtx.Done():
		}
	}
	go func() {
		for {
			select {
			case envelope := <-sendCh:
				if envelope == nil {
					continue
				}
				if err := stream.Send(envelope); err != nil {
					select {
					case writeErr <- err:
					default:
					}
					cancelConnection()
					return
				}
			case <-connectionCtx.Done():
				return
			}
		}
	}()

	if cc.Menu != nil {
		send("", &commandpb.ProtocolMessage{Message: &commandpb.ProtocolMessage_Catalog{Catalog: &commandpb.Catalog{Commands: cc.Menu()}}})
	}
	stats := NewAgentStatsTracker()
	if cc.AgentSubscribe != nil {
		unsubscribe := cc.AgentSubscribe(func(event *aop.Event) {
			if next, changed := stats.Observe(event); changed {
				send("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentStats{AgentStats: next}})
			}
			replyTo := ""
			if event.GetToolResult() != nil {
				replyTo = event.GetToolResult().GetCallId()
			}
			send(replyTo, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: event}})
		})
		defer unsubscribe()
	}
	if detach := attachToolEvents(cc.DataBus, cc.SCO, send); detach != nil {
		defer detach()
	}
	if cc.Status != nil {
		initial := cc.Status()
		if initial != nil {
			send("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentStatus{AgentStatus: initial}})
		}
		go func(last *aop.AgentStatus) {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					next := cc.Status()
					if next != nil && !protobuf.Equal(next, last) {
						send("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentStatus{AgentStatus: next}})
						last = protobuf.Clone(next).(*aop.AgentStatus)
					}
				case <-connectionCtx.Done():
					return
				}
			}
		}(cloneAgentStatus(initial))
	}

	var router *pty.Router
	if cc.PTYRouter != nil {
		router, err = cc.PTYRouter()
	} else {
		router = NewPTYRouter(cc.Registry)
	}
	if err != nil {
		return err
	}
	defer router.Close()
	if cc.PTYRouter == nil {
		if manager := RegistryPTYManager(cc.Registry); manager != nil {
			unsubscribe := SubscribePTYSessions(connectionCtx, manager, router, func(frame pty.Frame) {
				send("", terminalcodec.ToProto(frame))
			})
			defer unsubscribe()
		}
	}

	var operationsMu sync.Mutex
	operations := make(map[string]context.CancelFunc)
	namespaceMux, err := newAgentConnectionNamespaceMux(cc, router, send, &operationsMu, operations)
	if err != nil {
		return fmt.Errorf("register connection namespaces: %w", err)
	}
	for {
		envelope, err := stream.Recv()
		if err != nil {
			select {
			case writerErr := <-writeErr:
				return writerErr
			default:
			}
			return err
		}
		handled, err := namespaceMux.Dispatch(connectionCtx, envelope, func(*aop.Envelope) error { return nil })
		if err != nil {
			send(envelope.GetId(), protocolFailure("INVALID_PAYLOAD", err.Error()))
			continue
		}
		if !handled {
			send(envelope.GetId(), protocolFailure("UNSUPPORTED_NAMESPACE", "unsupported AOP namespace"))
		}
	}
}

func cloneAgentStatus(value *aop.AgentStatus) *aop.AgentStatus {
	if value == nil {
		return nil
	}
	return protobuf.Clone(value).(*aop.AgentStatus)
}

func newAgentConnectionNamespaceMux(
	cc connectionConfig,
	router *pty.Router,
	send func(string, protobuf.Message),
	operationsMu *sync.Mutex,
	operations map[string]context.CancelFunc,
) (*aop.NamespaceMux, error) {
	mux := aop.NewNamespaceMux()
	if err := mux.Register(&aop.ProtocolMessage{}, func(ctx context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		handleAgentCoreMessage(ctx, cc, envelope, message.(*aop.ProtocolMessage), send, operationsMu, operations)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mux.Register(&commandpb.ProtocolMessage{}, func(ctx context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		handleAgentCommandMessage(ctx, cc, envelope, message.(*commandpb.ProtocolMessage), send)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mux.Register(&toolpb.ProtocolMessage{}, func(ctx context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		handleAgentToolMessage(ctx, cc, envelope, message.(*toolpb.ProtocolMessage), send, operationsMu, operations)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mux.Register(&filepb.ProtocolMessage{}, func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		handleAgentFileMessage(cc, envelope, message.(*filepb.ProtocolMessage), send)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mux.Register(&execpb.ProtocolMessage{}, func(ctx context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		handleAgentExecMessage(ctx, cc, envelope, message.(*execpb.ProtocolMessage), send, operationsMu, operations)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mux.Register(&reloadpb.ProtocolMessage{}, func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		handleAgentReloadMessage(cc, envelope, message.(*reloadpb.ProtocolMessage), send)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mux.Register(&ptypb.ProtocolMessage{}, func(ctx context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		handleAgentPTYMessage(ctx, router, envelope, message.(*ptypb.ProtocolMessage), send)
		return nil
	}); err != nil {
		return nil, err
	}
	return mux, nil
}

func handleAgentCoreMessage(
	ctx context.Context,
	cc connectionConfig,
	envelope *aop.Envelope,
	value *aop.ProtocolMessage,
	send func(string, protobuf.Message),
	operationsMu *sync.Mutex,
	operations map[string]context.CancelFunc,
) {
	replyTo := envelope.GetId()
	fail := func(message string) { send(replyTo, protocolFailure("OPERATION_FAILED", message)) }
	switch payload := value.Message.(type) {
	case *aop.ProtocolMessage_OpenSessionRequest:
		if cc.Chat == nil {
			fail("chat handler is unavailable")
			return
		}
		send(replyTo, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionResponse{OpenSessionResponse: cc.Chat.OpenSession(ctx, payload.OpenSessionRequest)}})
	case *aop.ProtocolMessage_RunTurnRequest:
		if cc.Chat == nil {
			fail("chat handler is unavailable")
			return
		}
		send(replyTo, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_RunTurnResponse{RunTurnResponse: cc.Chat.RunTurn(ctx, payload.RunTurnRequest)}})
	case *aop.ProtocolMessage_CancelTurnRequest:
		if cc.Chat == nil {
			fail("chat handler is unavailable")
			return
		}
		send(replyTo, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CancelTurnResponse{CancelTurnResponse: cc.Chat.CancelTurn(payload.CancelTurnRequest)}})
	case *aop.ProtocolMessage_CloseSessionRequest:
		if cc.Chat == nil {
			fail("chat handler is unavailable")
			return
		}
		send(replyTo, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CloseSessionResponse{CloseSessionResponse: cc.Chat.CloseSession(ctx, payload.CloseSessionRequest)}})
	case *aop.ProtocolMessage_CancelOperation:
		operationsMu.Lock()
		cancel := operations[payload.CancelOperation.GetTargetId()]
		operationsMu.Unlock()
		if cancel != nil {
			cancel()
		}
	default:
		fail("unsupported AOP core message")
	}
}

func handleAgentCommandMessage(ctx context.Context, cc connectionConfig, envelope *aop.Envelope, value *commandpb.ProtocolMessage, send func(string, protobuf.Message)) {
	replyTo := envelope.GetId()
	fail := func(message string) { send(replyTo, protocolFailure("OPERATION_FAILED", message)) }
	request := value.GetRequest()
	if request == nil {
		fail("unsupported AIScan command message")
		return
	}
	go func() {
		if cc.Chat == nil {
			fail("command handler is unavailable")
			return
		}
		result, err := cc.Chat.Command(ctx, request)
		if err != nil {
			fail(err.Error())
			return
		}
		send(replyTo, &commandpb.ProtocolMessage{Message: &commandpb.ProtocolMessage_Result{Result: result}})
	}()
}

func handleAgentToolMessage(ctx context.Context, cc connectionConfig, envelope *aop.Envelope, value *toolpb.ProtocolMessage, send func(string, protobuf.Message), operationsMu *sync.Mutex, operations map[string]context.CancelFunc) {
	replyTo := envelope.GetId()
	fail := func(message string) { send(replyTo, protocolFailure("OPERATION_FAILED", message)) }
	request := value.GetCall()
	if request == nil || request.Call == nil {
		fail("unsupported AOP tool message")
		return
	}
	operationID := envelope.GetId()
	taskCtx, taskCancel := context.WithCancel(ctx)
	trackOperation(operationsMu, operations, operationID, taskCancel)
	go func() {
		defer finishOperation(operationsMu, operations, operationID, taskCancel)
		event, err := executeToolRequest(taskCtx, operationID, request, cc.Registry, cc.DataBus)
		if err != nil {
			fail(err.Error())
			return
		}
		send(replyTo, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: event}})
	}()
}

func handleAgentFileMessage(cc connectionConfig, envelope *aop.Envelope, value *filepb.ProtocolMessage, send func(string, protobuf.Message)) {
	replyTo := envelope.GetId()
	fail := func(message string) { send(replyTo, protocolFailure("OPERATION_FAILED", message)) }
	switch payload := value.Message.(type) {
	case *filepb.ProtocolMessage_ReadRequest:
		go sendFileResult(replyTo, fileRead(payload.ReadRequest, workingDir(cc.Runtime)), send)
	case *filepb.ProtocolMessage_WriteRequest:
		go sendFileResult(replyTo, fileWrite(payload.WriteRequest, workingDir(cc.Runtime)), send)
	case *filepb.ProtocolMessage_ListRequest:
		if !cc.RunnerFileRPC {
			fail("file list is unavailable")
			return
		}
		go sendFileResult(replyTo, fileList(payload.ListRequest, workingDir(cc.Runtime)), send)
	case *filepb.ProtocolMessage_MkdirRequest:
		if !cc.RunnerFileRPC {
			fail("file mkdir is unavailable")
			return
		}
		go sendFileResult(replyTo, fileMkdir(payload.MkdirRequest, workingDir(cc.Runtime)), send)
	case *filepb.ProtocolMessage_UploadRequest:
		go func() {
			if cc.Chat == nil {
				fail("upload handler is unavailable")
				return
			}
			result, err := cc.Chat.Upload(payload.UploadRequest)
			if err != nil {
				fail(err.Error())
				return
			}
			send(replyTo, &filepb.ProtocolMessage{Message: &filepb.ProtocolMessage_Result{Result: result}})
		}()
	default:
		fail("unsupported AOP file message")
	}
}

func handleAgentExecMessage(ctx context.Context, cc connectionConfig, envelope *aop.Envelope, value *execpb.ProtocolMessage, send func(string, protobuf.Message), operationsMu *sync.Mutex, operations map[string]context.CancelFunc) {
	replyTo := envelope.GetId()
	fail := func(message string) { send(replyTo, protocolFailure("OPERATION_FAILED", message)) }
	request := value.GetRequest()
	if request == nil {
		fail("unsupported AOP exec message")
		return
	}
	operationID := envelope.GetId()
	taskCtx, taskCancel := context.WithCancel(ctx)
	trackOperation(operationsMu, operations, operationID, taskCancel)
	go func() {
		defer finishOperation(operationsMu, operations, operationID, taskCancel)
		handleExecRequest(taskCtx, request, workingDir(cc.Runtime), replyTo, send)
	}()
}

func handleAgentReloadMessage(cc connectionConfig, envelope *aop.Envelope, value *reloadpb.ProtocolMessage, send func(string, protobuf.Message)) {
	replyTo := envelope.GetId()
	fail := func(message string) { send(replyTo, protocolFailure("OPERATION_FAILED", message)) }
	request := value.GetRequest()
	if request == nil || request.Config == nil || cc.Chat == nil {
		fail("config reload request is unavailable")
		return
	}
	result, status := cc.Chat.ReloadConfig(request.Config)
	if status != nil {
		send("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentStatus{AgentStatus: status}})
	}
	send(replyTo, &reloadpb.ProtocolMessage{Message: &reloadpb.ProtocolMessage_Result{Result: result}})
}

func handleAgentPTYMessage(ctx context.Context, router *pty.Router, envelope *aop.Envelope, value *ptypb.ProtocolMessage, send func(string, protobuf.Message)) {
	if router == nil {
		send(envelope.GetId(), protocolFailure("OPERATION_FAILED", "PTY router is unavailable"))
		return
	}
	router.Handle(ctx, terminalcodec.FromProto(value), func(out pty.Frame) {
		send(envelope.GetId(), terminalcodec.ToProto(out))
	})
}

func workingDir(runtimeInfo *aop.AgentRuntimeInfo) string {
	if runtimeInfo == nil {
		return ""
	}
	return runtimeInfo.WorkingDir
}

func protocolFailure(code, message string) *aop.ProtocolMessage {
	return &aop.ProtocolMessage{Message: &aop.ProtocolMessage_ProtocolError{ProtocolError: &aop.ProtocolError{Code: code, Message: message}}}
}

func trackOperation(mu *sync.Mutex, operations map[string]context.CancelFunc, id string, cancel context.CancelFunc) {
	mu.Lock()
	operations[id] = cancel
	mu.Unlock()
}

func finishOperation(mu *sync.Mutex, operations map[string]context.CancelFunc, id string, cancel context.CancelFunc) {
	cancel()
	mu.Lock()
	delete(operations, id)
	mu.Unlock()
}

func executeToolRequest(ctx context.Context, operationID string, request *toolpb.Call, executor aopToolExecutor, dataBus *eventbus.Bus[output.ToolDataEvent]) (*aop.Event, error) {
	if request == nil || request.Call == nil || operationID == "" {
		return nil, fmt.Errorf("tool call correlation is invalid")
	}
	call := request.Call
	if call.Id == "" {
		call.Id = operationID
	}
	if call.Id != operationID {
		return nil, fmt.Errorf("tool call id must match envelope id")
	}
	if strings.TrimSpace(call.Name) == "" {
		return nil, fmt.Errorf("tool name is required")
	}
	if call.WorkingDirectory != "" {
		ctx = tool.ContextWithInvocation(ctx, tool.Invocation{WorkDir: call.WorkingDirectory})
	}
	ctx = output.ContextWithCallID(ctx, operationID)
	started := time.Now()
	result, execErr := executeCall(ctx, executor, call, dataBus, operationID)
	if result == nil {
		result = &aop.ToolResult{}
	}
	if execErr != nil {
		result.IsError = true
		result.Output = []*aop.Content{aop.Text(execErr.Error())}
	}
	result.CallId = call.Id
	result.Name = call.Name
	result.DurationMs = uint64(time.Since(started).Milliseconds())
	return &aop.Event{
		Id: nextEnvelopeID("event"), EmittedAt: timestamppb.Now(), SessionId: request.SessionId,
		TurnId: request.TurnId, Emitter: "aiscan.agent", Payload: &aop.Event_ToolResult{ToolResult: result},
	}, nil
}

type fileResultValue struct {
	result *filepb.Result
	err    error
}

func resolveFileRPCPath(baseDir, path string) string {
	if filepath.IsAbs(path) || baseDir == "" {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

func sendFileResult(replyTo string, value fileResultValue, send func(string, protobuf.Message)) {
	if value.err != nil {
		send(replyTo, protocolFailure("FILE_OPERATION_FAILED", value.err.Error()))
		return
	}
	send(replyTo, &filepb.ProtocolMessage{Message: &filepb.ProtocolMessage_Result{Result: value.result}})
}

func fileRead(req *filepb.ReadRequest, base string) fileResultValue {
	result := &filepb.Result{}
	if req != nil {
		result.Path = req.Path
	}
	if req == nil || req.Path == "" {
		return fileResultValue{result: result, err: fmt.Errorf("file path is required")}
	}
	data, err := os.ReadFile(resolveFileRPCPath(base, req.Path))
	result.Data = data
	result.Size = int64(len(data))
	return fileResultValue{result: result, err: err}
}

func fileWrite(req *filepb.WriteRequest, base string) fileResultValue {
	result := &filepb.Result{}
	if req != nil {
		result.Path = req.Path
		result.Size = int64(len(req.Data))
	}
	if req == nil || req.Path == "" {
		return fileResultValue{result: result, err: fmt.Errorf("file path is required")}
	}
	path := resolveFileRPCPath(base, req.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fileResultValue{result: result, err: err}
	}
	return fileResultValue{result: result, err: os.WriteFile(path, req.Data, 0o644)}
}

func fileList(req *filepb.ListRequest, base string) fileResultValue {
	result := &filepb.Result{}
	if req != nil {
		result.Path = req.Path
	}
	if result.Path == "" {
		result.Path = "."
	}
	entries, err := os.ReadDir(resolveFileRPCPath(base, result.Path))
	if err != nil {
		return fileResultValue{result: result, err: err}
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return fileResultValue{result: result, err: err}
		}
		result.Entries = append(result.Entries, &filepb.Entry{Name: entry.Name(), IsDirectory: entry.IsDir(), Size: info.Size()})
	}
	return fileResultValue{result: result}
}

func fileMkdir(req *filepb.MkdirRequest, base string) fileResultValue {
	result := &filepb.Result{}
	if req != nil {
		result.Path = req.Path
	}
	if req == nil || req.Path == "" {
		return fileResultValue{result: result, err: fmt.Errorf("directory path is required")}
	}
	return fileResultValue{result: result, err: os.MkdirAll(resolveFileRPCPath(base, req.Path), 0o755)}
}

func handleExecRequest(ctx context.Context, req *execpb.Request, base, replyTo string, send func(string, protobuf.Message)) {
	if req == nil || strings.TrimSpace(req.Command) == "" {
		send(replyTo, protocolFailure("INVALID_ARGUMENT", "command is required"))
		return
	}
	runCtx := ctx
	cancel := func() {}
	if req.TimeoutSeconds > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutSeconds)*time.Second)
	}
	defer cancel()
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.CommandContext(runCtx, "cmd.exe", "/C", req.Command)
	} else {
		command = exec.CommandContext(runCtx, "/bin/sh", "-c", req.Command)
	}
	if req.Cwd != "" {
		command.Dir = resolveFileRPCPath(base, req.Cwd)
	} else if base != "" {
		command.Dir = base
	}
	command.Env = os.Environ()
	for key, value := range req.Env {
		command.Env = append(command.Env, key+"="+value)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if stdout.Len() > 0 {
		send(replyTo, &execpb.ProtocolMessage{Message: &execpb.ProtocolMessage_Output{Output: &execpb.Output{Stream: execpb.Stream_STREAM_STDOUT, Data: stdout.Bytes()}}})
	}
	if stderr.Len() > 0 {
		send(replyTo, &execpb.ProtocolMessage{Message: &execpb.ProtocolMessage_Output{Output: &execpb.Output{Stream: execpb.Stream_STREAM_STDERR, Data: stderr.Bytes()}}})
	}
	result := &execpb.Result{State: "completed"}
	if err != nil {
		var exitErr *exec.ExitError
		switch {
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			result.ExitCode = -1
			result.State = "killed"
			result.KillCause = "timeout"
		case errors.Is(runCtx.Err(), context.Canceled):
			result.ExitCode = -1
			result.State = "killed"
			result.KillCause = "canceled"
		case errors.As(err, &exitErr):
			result.ExitCode = int32(exitErr.ExitCode())
		default:
			send(replyTo, protocolFailure("EXEC_FAILED", err.Error()))
			return
		}
	}
	send(replyTo, &execpb.ProtocolMessage{Message: &execpb.ProtocolMessage_Result{Result: result}})
}
