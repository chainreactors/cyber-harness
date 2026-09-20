package node

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chainreactors/cyber/agent"
	aop "github.com/chainreactors/cyber/aop"
	filepb "github.com/chainreactors/cyber/aop/file"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/operation"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	types "github.com/chainreactors/cyber/core/types"
	"github.com/chainreactors/cyber/pkg/aopconn"
	"github.com/chainreactors/cyber/pkg/aopws"

	toolnode "github.com/chainreactors/cyber/pkg/node/tool"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
)

const (
	// Stay below common 60-second proxy and NAT idle timeouts.
	websocketPingPeriod = 30 * time.Second
	// Bound a stalled application or control-frame write.
	websocketWriteWait = 10 * time.Second

	websocketPongWait    = 3 * websocketPingPeriod
	reconnectStableAfter = websocketPongWait + websocketPingPeriod
)

func dialProtoWebSocket(ctx context.Context, cc connectionConfig) (*aopws.Stream, error) {
	dialURL, accessKey := SplitAccessKey(cc.ServerURL)
	if cc.Token != "" {
		accessKey = cc.Token
	}
	path := cc.WSPath
	if path == "" {
		path = toolnode.DefaultWSPath
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
		if response != nil {
			return nil, &websocketHandshakeError{statusCode: response.StatusCode, cause: err}
		}
		return nil, err
	}
	encoding := aopws.Binary
	if cc.JSONFrames {
		encoding = aopws.ProtoJSON
	}
	return aopws.New(ctx, conn, aopws.Options{
		Encoding:     encoding,
		WriteTimeout: websocketWriteWait,
		PingInterval: websocketPingPeriod,
		PongTimeout:  websocketPongWait,
	})
}

func shouldResetReconnectBackoff(connectedAt, disconnectedAt time.Time) bool {
	return !connectedAt.IsZero() && disconnectedAt.Sub(connectedAt) >= reconnectStableAfter
}

func connectGenerated(ctx context.Context, cc connectionConfig) error {
	logger := cc.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	cc.Logger = logger
	attempt := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		stream, err := dialProtoWebSocket(ctx, cc)
		connectedAt := time.Time{}
		if err == nil {
			connectedAt = time.Now()
			err = serveAgentConnection(ctx, cc, logger, stream)
			_ = stream.Close()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if shouldResetReconnectBackoff(connectedAt, time.Now()) {
			attempt = 0
		}
		delay := agent.RetryDelay(attempt)
		attempt++
		logger.Warnf("connection lost (attempt %d), retrying in %v: %s", attempt, delay, describeConnectionFailure(err))
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
	if cc.Agent == nil {
		return fmt.Errorf("agent event endpoint is required")
	}
	executor := connectionExecutor(cc)
	if executor == nil {
		return fmt.Errorf("tool executor is nil")
	}
	cc.Executor = executor
	hello, err := BuildHello(cc.Name, cc.Executor, cc.NodeID, cc.Runtime)
	if err != nil {
		return err
	}
	if len(cc.Capabilities) > 0 {
		hello.Capabilities = append([]string(nil), cc.Capabilities...)
	} else if cc.Chat == nil {
		hello.Capabilities = []string{"pty", "file", "tool", "sco"}
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
	if !ok || acceptedEnvelope.ReplyTo != helloEnvelope.Id {
		return fmt.Errorf("expected AOP enrollment response")
	}
	if coreAccepted.GetProtocolError() != nil {
		rejected := coreAccepted.GetProtocolError()
		code := strings.TrimSpace(rejected.GetCode())
		message := strings.TrimSpace(rejected.GetMessage())
		switch {
		case code != "" && message != "":
			return fmt.Errorf("AOP enrollment rejected (%s): %s", code, message)
		case code != "":
			return fmt.Errorf("AOP enrollment rejected (%s)", code)
		case message != "":
			return fmt.Errorf("AOP enrollment rejected: %s", message)
		default:
			return fmt.Errorf("AOP enrollment rejected")
		}
	}
	if coreAccepted.GetAgentAccepted() == nil {
		return fmt.Errorf("expected AOP agent acceptance")
	}
	connection, err := aopconn.NewConnection(ctx, stream)
	if err != nil {
		return err
	}
	defer connection.Close()
	connectionCtx := connection.Context()
	send := func(replyTo string, message protobuf.Message) {
		if err := connection.Send(aop.Reply(replyTo, message)); err != nil {
			logger.Debugf("send AOP message: %v", err)
		}
	}
	calls := &toolnode.CallHandler{Executor: executor, Progress: cc.Progress, Emitter: cc.Name, Logger: logger, Send: send, Publish: cc.Agent.Publish}
	namespaceMux, err := newAgentConnectionNamespaceMux(connectionCtx, cc, send, calls)
	if err != nil {
		return err
	}
	defer func() { namespaceMux.Cancel(); _ = namespaceMux.Close(context.Background()) }()
	stats := NewAgentStatsTracker()
	unsubscribe := cc.Agent.Observe(coreevents.ObserverFunc(func(event *aop.Event) {
		if next, changed := stats.Observe(event); changed {
			send("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentStats{AgentStats: next}})
		}
		calls.Forward(event)
	}))
	if unsubscribe == nil {
		return fmt.Errorf("agent event subscription is required")
	}
	defer unsubscribe.Cancel()
	if cc.Progress != nil {
		sub := cc.Progress.Subscribe(calls.ForwardProgress)
		defer sub.Cancel()
	}

	defer calls.Close()

	// The catalog is the first post-handshake message the hub treats as a
	// readiness signal. Attach event and progress subscribers before publishing
	// it so callers cannot emit into the small acceptance-to-subscribe gap.
	if cc.Menu != nil {
		send("", &types.CommandProtocolMessage{Message: &types.CommandProtocolMessage_Catalog{Catalog: &types.CommandCatalog{Commands: cc.Menu()}}})
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
						last = protobuf.CloneOf(next)
					}
				case <-connectionCtx.Done():
					return
				}
			}
		}(cloneAgentStatus(initial))
	}

	return connection.Run(func(_ context.Context, envelope *aop.Envelope, reply aop.SendFunc) error {
		if calls.Cancel(envelope) {
			return nil
		}
		handled, err := namespaceMux.Dispatch(envelope, reply)
		if err != nil {
			send(envelope.GetId(), protocolFailure("INVALID_PAYLOAD", err.Error()))
		} else if !handled {
			send(envelope.GetId(), protocolFailure("UNSUPPORTED_NAMESPACE", "unsupported AOP namespace"))
		}
		return nil
	})
}

func cloneAgentStatus(value *aop.AgentStatus) *aop.AgentStatus {
	if value == nil {
		return nil
	}
	return protobuf.CloneOf(value)
}

func newAgentConnectionNamespaceMux(
	connectionCtx context.Context,
	cc connectionConfig,
	send func(string, protobuf.Message),
	calls *toolnode.CallHandler,
) (*aop.NamespaceMux, error) {
	mux := aop.NewNamespaceMux(connectionCtx)
	ok := false
	defer func() {
		if !ok {
			_ = mux.Close(context.Background())
		}
	}()
	if err := mux.Register(&toolpb.ProtocolMessage{}, calls.Handle); err != nil {
		return nil, err
	}
	if err := mux.Register(&filepb.ProtocolMessage{}, func(ctx context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, ok := message.(*filepb.ProtocolMessage)
		if !ok {
			return fmt.Errorf("unexpected file namespace message %T", message)
		}
		handleAgentFileMessage(ctx, cc, envelope, value, send)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mux.Register(&types.ReloadProtocolMessage{}, func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, ok := message.(*types.ReloadProtocolMessage)
		if !ok {
			return fmt.Errorf("unexpected reload namespace message %T", message)
		}
		handleAgentReloadMessage(cc, envelope, value, send)
		return nil
	}); err != nil {
		return nil, err
	}
	if cc.RegisterNamespaces != nil {
		if err := cc.RegisterNamespaces(mux); err != nil {
			return nil, err
		}
	}
	ok = true
	return mux, nil
}

func connectionExecutor(cc connectionConfig) coretool.Executor {
	if cc.Executor != nil {
		return cc.Executor
	}
	return coretool.EmptyExecutor()
}

func handleAgentFileMessage(ctx context.Context, cc connectionConfig, envelope *aop.Envelope, value *filepb.ProtocolMessage, send func(string, protobuf.Message)) {
	replyTo := envelope.GetId()
	fail := func(message string) { send(replyTo, protocolFailure("OPERATION_FAILED", message)) }
	switch payload := value.Message.(type) {
	case *filepb.ProtocolMessage_ReadRequest:
		if !cc.RunnerFileRPC {
			fail("file read is unavailable")
			return
		}
		go func() {
			base := workingDir(cc.Runtime)
			accessCtx, finish := operation.Begin(operation.ContextWithInvocation(ctx, operation.Invocation{CallID: replyTo, WorkDir: base}), "file", "read")
			value := fileRead(payload.ReadRequest, base)
			observeControlAccess(cc.Hooks, accessCtx, filepb.AccessOp_ACCESS_OP_READ, base, payload.ReadRequest.GetPath(), &value)
			sendFileResult(replyTo, value, send)
			finish(value.err)
		}()
	case *filepb.ProtocolMessage_WriteRequest:
		if !cc.RunnerFileRPC {
			fail("file write is unavailable")
			return
		}
		go func() {
			base := workingDir(cc.Runtime)
			accessCtx, finish := operation.Begin(operation.ContextWithInvocation(ctx, operation.Invocation{CallID: replyTo, WorkDir: base}), "file", "write")
			value := fileWrite(payload.WriteRequest, base)
			observeControlAccess(cc.Hooks, accessCtx, filepb.AccessOp_ACCESS_OP_WRITE, base, payload.WriteRequest.GetPath(), &value)
			sendFileResult(replyTo, value, send)
			finish(value.err)
		}()
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

func handleAgentReloadMessage(cc connectionConfig, envelope *aop.Envelope, value *types.ReloadProtocolMessage, send func(string, protobuf.Message)) {
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
	send(replyTo, &types.ReloadProtocolMessage{Message: &types.ReloadProtocolMessage_Result{Result: result}})
	if result.Ok && cc.Chat.commit != nil {
		cc.Chat.commit()
	}
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
	requestPath, offset, limit := req.GetPath(), req.GetOffset(), req.GetLimit()
	result.Path = requestPath
	if offset < 0 {
		return fileResultValue{result: result, err: fmt.Errorf("file offset cannot be negative")}
	}
	if limit < 0 {
		return fileResultValue{result: result, err: fmt.Errorf("file read limit cannot be negative")}
	}
	path := resolveFileRPCPath(base, requestPath)
	file, err := os.Open(path)
	if err != nil {
		return fileResultValue{result: result, err: err}
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fileResultValue{result: result, err: err}
	}
	if info.IsDir() {
		return fileResultValue{result: result, err: fmt.Errorf("file path is a directory")}
	}
	if offset > info.Size() {
		return fileResultValue{result: result, err: fmt.Errorf("file offset %d exceeds size %d", offset, info.Size())}
	}
	result.Filename = info.Name()
	result.Size = info.Size()
	result.Offset = offset
	if limit == 0 {
		data, readErr := io.ReadAll(file)
		result.Data = data
		result.Offset = 0
		result.Eof = readErr == nil
		result.MediaType = detectFileMediaType(path, data)
		return fileResultValue{result: result, err: readErr}
	}
	readLimit := min(int64(limit), int64(maxFileReadChunkBytes))
	remaining := info.Size() - offset
	if readLimit > remaining {
		readLimit = remaining
	}
	data := make([]byte, int(readLimit))
	n, readErr := file.ReadAt(data, offset)
	if readErr != nil && readErr != io.EOF {
		return fileResultValue{result: result, err: readErr}
	}
	result.Data = data[:n]
	result.Eof = offset+int64(n) >= info.Size()
	result.MediaType = detectFileMediaType(path, result.Data)
	return fileResultValue{result: result}
}

const maxFileReadChunkBytes int32 = 1 << 20

func detectFileMediaType(path string, data []byte) string {
	if value := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); value != "" {
		return value
	}
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	return "application/octet-stream"
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
