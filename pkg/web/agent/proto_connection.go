package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/tool"
	terminalcodec "github.com/chainreactors/aiscan/pkg/web/terminal"
	"github.com/chainreactors/utils/pty"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	protobuf "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type AgentServerStream interface {
	Context() context.Context
	Recv() (*transport.ServerFrame, error)
	Send(*transport.AgentFrame) error
}

type closeableAgentServerStream interface {
	AgentServerStream
	Close() error
}

type webSocketServerStream struct {
	ctx  context.Context
	conn *websocket.Conn
	mu   sync.Mutex
}

func (s *webSocketServerStream) Context() context.Context { return s.ctx }
func (s *webSocketServerStream) Close() error             { return s.conn.Close() }
func (s *webSocketServerStream) Recv() (*transport.ServerFrame, error) {
	_, data, err := s.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	frame := new(transport.ServerFrame)
	if err := protojson.Unmarshal(data, frame); err != nil {
		return nil, fmt.Errorf("decode server frame: %w", err)
	}
	return frame, nil
}
func (s *webSocketServerStream) Send(frame *transport.AgentFrame) error {
	data, err := protojson.Marshal(frame)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteMessage(websocket.TextMessage, data)
}

type grpcServerStream struct {
	transport.AgentTransportService_ConnectClient
	conn *grpc.ClientConn
}

func (s *grpcServerStream) Close() error { return s.conn.Close() }

func dialProtoWebSocket(ctx context.Context, cc connectionConfig) (closeableAgentServerStream, error) {
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
	return &webSocketServerStream{ctx: ctx, conn: conn}, nil
}

func dialProtoGRPC(ctx context.Context, cc connectionConfig) (closeableAgentServerStream, error) {
	rawURL, accessKey := SplitAccessKey(cc.ServerURL)
	if cc.Token != "" {
		accessKey = cc.Token
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid gRPC server URL %q", rawURL)
	}
	var creds credentials.TransportCredentials
	if strings.EqualFold(u.Scheme, "https") {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname()})
	} else {
		creds = insecure.NewCredentials()
	}
	conn, err := grpc.NewClient(u.Host, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, err
	}
	streamCtx := ctx
	if accessKey != "" {
		streamCtx = metadata.AppendToOutgoingContext(streamCtx, "authorization", "Bearer "+accessKey)
	}
	stream, err := transport.NewAgentTransportServiceClient(conn).Connect(streamCtx)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &grpcServerStream{AgentTransportService_ConnectClient: stream, conn: conn}, nil
}

func connectGenerated(ctx context.Context, cc connectionConfig, grpcTransport bool) error {
	logger := cc.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	attempt := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var stream closeableAgentServerStream
		var err error
		if grpcTransport {
			stream, err = dialProtoGRPC(ctx, cc)
		} else {
			stream, err = dialProtoWebSocket(ctx, cc)
		}
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

func serveAgentConnection(ctx context.Context, cc connectionConfig, logger telemetry.Logger, stream AgentServerStream) error {
	if cc.Registry == nil {
		return fmt.Errorf("command registry is nil")
	}
	hello, err := BuildHello(cc.Name, cc.Registry, cc.Node, cc.Runtime, cc.Status, cc.Menu, &transport.AgentStats{})
	if err != nil {
		return err
	}
	if err := stream.Send(&transport.AgentFrame{Payload: &transport.AgentFrame_Hello{Hello: hello}}); err != nil {
		return err
	}
	accepted, err := stream.Recv()
	if err != nil {
		return err
	}
	if accepted.GetAccepted() == nil {
		return fmt.Errorf("expected connection acceptance")
	}

	connectionCtx, cancelConnection := context.WithCancel(ctx)
	defer cancelConnection()
	sendCh := make(chan *transport.AgentFrame, 64)
	writeErr := make(chan error, 1)
	send := func(frame *transport.AgentFrame) {
		select {
		case sendCh <- frame:
		case <-connectionCtx.Done():
		}
	}
	go func() {
		for {
			select {
			case frame := <-sendCh:
				if err := stream.Send(frame); err != nil {
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

	stats := NewAgentStatsTracker()
	if cc.AgentSubscribe != nil {
		unsubscribe := cc.AgentSubscribe(func(event *aop.Event) {
			if next, changed := stats.Observe(event); changed {
				send(&transport.AgentFrame{Payload: &transport.AgentFrame_Stats{Stats: next}})
			}
			send(&transport.AgentFrame{CorrelationId: event.TurnId, Payload: &transport.AgentFrame_Event{Event: event}})
		})
		defer unsubscribe()
	}
	if detach := attachToolEvents(cc.DataBus, cc.SCO, send); detach != nil {
		defer detach()
	}
	if cc.Status != nil {
		go func(last *transport.AgentStatus) {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					next := cc.Status()
					if !protobuf.Equal(next, last) {
						send(&transport.AgentFrame{Payload: &transport.AgentFrame_Status{Status: next}})
						last = protobuf.Clone(next).(*transport.AgentStatus)
					}
				case <-connectionCtx.Done():
					return
				}
			}
		}(protobuf.Clone(hello.GetStatus()).(*transport.AgentStatus))
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
				send(&transport.AgentFrame{Payload: &transport.AgentFrame_Terminal{Terminal: terminalcodec.ToProto(frame)}})
			})
			defer unsubscribe()
		}
	}

	var operationsMu sync.Mutex
	operations := make(map[string]context.CancelFunc)
	for {
		frame, err := stream.Recv()
		if err != nil {
			select {
			case writerErr := <-writeErr:
				return writerErr
			default:
			}
			return err
		}
		switch payload := frame.Payload.(type) {
		case *transport.ServerFrame_OpenSession:
			if cc.Chat != nil {
				send(&transport.AgentFrame{CorrelationId: frame.CorrelationId, Payload: &transport.AgentFrame_OpenSession{OpenSession: cc.Chat.OpenSession(connectionCtx, payload.OpenSession)}})
			}
		case *transport.ServerFrame_RunTurn:
			if cc.Chat != nil {
				send(&transport.AgentFrame{CorrelationId: frame.CorrelationId, Payload: &transport.AgentFrame_RunTurn{RunTurn: cc.Chat.RunTurn(connectionCtx, payload.RunTurn)}})
			}
		case *transport.ServerFrame_CancelTurn:
			if cc.Chat != nil {
				send(&transport.AgentFrame{CorrelationId: frame.CorrelationId, Payload: &transport.AgentFrame_CancelTurn{CancelTurn: cc.Chat.CancelTurn(payload.CancelTurn)}})
			}
		case *transport.ServerFrame_CloseSession:
			if cc.Chat != nil {
				send(&transport.AgentFrame{CorrelationId: frame.CorrelationId, Payload: &transport.AgentFrame_CloseSession{CloseSession: cc.Chat.CloseSession(connectionCtx, payload.CloseSession)}})
			}
		case *transport.ServerFrame_Command:
			go func(request *transport.CommandRequest, correlation string) {
				if cc.Chat == nil {
					send(operationFailure(request.GetTaskId(), "command handler is unavailable"))
					return
				}
				result, err := cc.Chat.Command(connectionCtx, request)
				if err != nil {
					send(operationFailure(request.GetTaskId(), err.Error()))
					return
				}
				send(&transport.AgentFrame{CorrelationId: correlation, Payload: &transport.AgentFrame_CommandResult{CommandResult: result}})
			}(payload.Command, frame.CorrelationId)
		case *transport.ServerFrame_ToolCall:
			request := payload.ToolCall
			taskCtx, taskCancel := context.WithCancel(connectionCtx)
			trackOperation(&operationsMu, operations, request.GetTaskId(), taskCancel)
			go func() {
				defer finishOperation(&operationsMu, operations, request.GetTaskId(), taskCancel)
				event, err := executeToolRequest(taskCtx, request, cc.Registry, cc.DataBus)
				if err != nil {
					send(operationFailure(request.GetTaskId(), err.Error()))
					return
				}
				send(&transport.AgentFrame{CorrelationId: request.GetTaskId(), Payload: &transport.AgentFrame_Event{Event: event}})
			}()
		case *transport.ServerFrame_FileRead:
			go sendFileResult(frame.CorrelationId, fileRead(payload.FileRead, cc.Runtime.GetWorkingDir()), send)
		case *transport.ServerFrame_FileWrite:
			go sendFileResult(frame.CorrelationId, fileWrite(payload.FileWrite, cc.Runtime.GetWorkingDir()), send)
		case *transport.ServerFrame_FileList:
			if cc.RunnerFileRPC {
				go sendFileResult(frame.CorrelationId, fileList(payload.FileList, cc.Runtime.GetWorkingDir()), send)
			}
		case *transport.ServerFrame_FileMkdir:
			if cc.RunnerFileRPC {
				go sendFileResult(frame.CorrelationId, fileMkdir(payload.FileMkdir, cc.Runtime.GetWorkingDir()), send)
			}
		case *transport.ServerFrame_FileUpload:
			go func(request *transport.FileUploadRequest, correlation string) {
				if cc.Chat == nil {
					send(operationFailure(request.GetTaskId(), "upload handler is unavailable"))
					return
				}
				result, err := cc.Chat.Upload(request)
				if err != nil {
					send(operationFailure(request.GetTaskId(), err.Error()))
					return
				}
				send(&transport.AgentFrame{CorrelationId: correlation, Payload: &transport.AgentFrame_FileResult{FileResult: result}})
			}(payload.FileUpload, frame.CorrelationId)
		case *transport.ServerFrame_Exec:
			request := payload.Exec
			taskCtx, taskCancel := context.WithCancel(connectionCtx)
			trackOperation(&operationsMu, operations, request.GetTaskId(), taskCancel)
			go func() {
				defer finishOperation(&operationsMu, operations, request.GetTaskId(), taskCancel)
				handleExecRequest(taskCtx, request, cc.Runtime.GetWorkingDir(), send)
			}()
		case *transport.ServerFrame_CancelOperation:
			operationsMu.Lock()
			operationCancel := operations[payload.CancelOperation.GetTaskId()]
			operationsMu.Unlock()
			if operationCancel != nil {
				operationCancel()
			}
		case *transport.ServerFrame_ReloadConfig:
			if cc.Chat != nil {
				result, statusValue := cc.Chat.ReloadConfig(cc.ServerURL)
				if statusValue != nil {
					send(&transport.AgentFrame{Payload: &transport.AgentFrame_Status{Status: statusValue}})
				}
				send(&transport.AgentFrame{CorrelationId: frame.CorrelationId, Payload: &transport.AgentFrame_ConfigReload{ConfigReload: result}})
			}
		case *transport.ServerFrame_Terminal:
			router.Handle(connectionCtx, terminalcodec.FromProto(payload.Terminal), func(out pty.Frame) {
				send(&transport.AgentFrame{Payload: &transport.AgentFrame_Terminal{Terminal: terminalcodec.ToProto(out)}})
			})
		}
	}
}

func operationFailure(taskID, message string) *transport.AgentFrame {
	return &transport.AgentFrame{CorrelationId: taskID, Payload: &transport.AgentFrame_OperationError{OperationError: &transport.OperationError{TaskId: taskID, Message: message}}}
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

func executeToolRequest(ctx context.Context, request *transport.ToolCallRequest, executor aopToolExecutor, dataBus *eventbus.Bus[output.ToolDataEvent]) (*aop.Event, error) {
	if request == nil || request.Call == nil || request.TaskId == "" || request.Call.Id != request.TaskId {
		return nil, fmt.Errorf("tool call correlation is invalid")
	}
	call := request.Call
	if strings.TrimSpace(call.Name) == "" {
		return nil, fmt.Errorf("tool name is required")
	}
	if call.WorkingDirectory != "" {
		ctx = tool.ContextWithInvocation(ctx, tool.Invocation{WorkDir: call.WorkingDirectory})
	}
	ctx = output.ContextWithCallID(ctx, request.TaskId)
	started := time.Now()
	result, execErr := executeCall(ctx, executor, call, dataBus, request.TaskId)
	text := result.Text()
	if execErr != nil {
		text = execErr.Error()
	}
	content := []*aop.Content{aop.Text(text)}
	for _, block := range result.Content {
		if block.Type != "image" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(block.Base64Data)
		if err == nil {
			content = append(content, aop.Image(block.MimeType, data))
		}
	}
	detail, _ := aop.JSONValue(result.Details)
	event := &aop.Event{Id: request.TaskId, EmittedAt: timestamppb.Now(), SessionId: request.SessionId, TurnId: request.TurnId, Emitter: "aiscan.agent", Payload: &aop.Event_ToolResult{ToolResult: &aop.ToolResult{CallId: call.Id, Name: call.Name, Output: content, Detail: detail, Terminate: result.Terminate, IsError: execErr != nil || result.IsError, DurationMs: uint64(time.Since(started).Milliseconds())}}}
	return event, nil
}

type fileResultValue struct {
	result *transport.FileResult
	err    error
}

func resolveFileRPCPath(baseDir, path string) string {
	if filepath.IsAbs(path) || baseDir == "" {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

func sendFileResult(correlation string, value fileResultValue, send func(*transport.AgentFrame)) {
	if value.err != nil {
		taskID := ""
		if value.result != nil {
			taskID = value.result.TaskId
		}
		send(operationFailure(taskID, value.err.Error()))
		return
	}
	send(&transport.AgentFrame{CorrelationId: correlation, Payload: &transport.AgentFrame_FileResult{FileResult: value.result}})
}
func fileRead(req *transport.FileReadRequest, base string) fileResultValue {
	result := &transport.FileResult{}
	if req != nil {
		result.TaskId = req.TaskId
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
func fileWrite(req *transport.FileWriteRequest, base string) fileResultValue {
	result := &transport.FileResult{}
	if req != nil {
		result.TaskId = req.TaskId
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
func fileList(req *transport.FileListRequest, base string) fileResultValue {
	result := &transport.FileResult{}
	if req != nil {
		result.TaskId = req.TaskId
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
		result.Entries = append(result.Entries, &transport.FileEntry{Name: entry.Name(), IsDirectory: entry.IsDir(), Size: info.Size()})
	}
	return fileResultValue{result: result}
}
func fileMkdir(req *transport.FileMkdirRequest, base string) fileResultValue {
	result := &transport.FileResult{}
	if req != nil {
		result.TaskId = req.TaskId
		result.Path = req.Path
	}
	if req == nil || req.Path == "" {
		return fileResultValue{result: result, err: fmt.Errorf("directory path is required")}
	}
	return fileResultValue{result: result, err: os.MkdirAll(resolveFileRPCPath(base, req.Path), 0o755)}
}

func handleExecRequest(ctx context.Context, req *transport.ExecRequest, base string, send func(*transport.AgentFrame)) {
	if req == nil || strings.TrimSpace(req.Command) == "" {
		send(operationFailure(req.GetTaskId(), "command is required"))
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
		send(&transport.AgentFrame{CorrelationId: req.TaskId, Payload: &transport.AgentFrame_ExecOutput{ExecOutput: &transport.ExecOutput{TaskId: req.TaskId, Stream: transport.ExecStream_EXEC_STREAM_STDOUT, Data: stdout.Bytes()}}})
	}
	if stderr.Len() > 0 {
		send(&transport.AgentFrame{CorrelationId: req.TaskId, Payload: &transport.AgentFrame_ExecOutput{ExecOutput: &transport.ExecOutput{TaskId: req.TaskId, Stream: transport.ExecStream_EXEC_STREAM_STDERR, Data: stderr.Bytes()}}})
	}
	result := &transport.ExecResult{TaskId: req.TaskId, State: "completed"}
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
			send(operationFailure(req.TaskId, err.Error()))
			return
		}
	}
	send(&transport.AgentFrame{CorrelationId: req.TaskId, Payload: &transport.AgentFrame_ExecResult{ExecResult: result}})
}
