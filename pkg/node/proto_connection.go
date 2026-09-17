package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chainreactors/cyber/agent"
	aop "github.com/chainreactors/cyber/aop"
	execpb "github.com/chainreactors/cyber/aop/exec"
	filepb "github.com/chainreactors/cyber/aop/file"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/eventbus"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/operation"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/core/tool"
	types "github.com/chainreactors/cyber/core/types"
	"github.com/chainreactors/cyber/pkg/aopws"
	toolnode "github.com/chainreactors/cyber/pkg/node/tool"
	toolset "github.com/chainreactors/cyber/pkg/toolset"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
)

func attachToolProgress(progressBus *eventbus.Bus[*toolpb.Progress], send func(string, protobuf.Message)) *eventbus.Subscription[*toolpb.Progress] {
	if progressBus == nil {
		return nil
	}
	unsubscribe := progressBus.Subscribe(func(progress *toolpb.Progress) {
		if progress != nil && progress.Text != "" {
			send(progress.CallId, &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Progress{Progress: protobuf.CloneOf(progress)}})
		}
	})
	return unsubscribe
}

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
					if closer, ok := stream.(io.Closer); ok {
						_ = closer.Close()
					}
					cancelConnection()
					return
				}
			case <-connectionCtx.Done():
				return
			}
		}
	}()

	// operations tracks live tool/exec calls by id; sealed remembers the ids
	// whose artifact window this connection has already closed — either because
	// the terminal is about to be sent (handleAgentToolMessage) or because the
	// hub canceled the call. The artifact-forwarding
	// subscriber below reads sealed to drop a streaming tool's trailing
	// artifacts. Both are declared here (rather than just above the mux) so the
	// subscriber can see them.
	var operationsMu sync.Mutex
	operations := make(map[string]context.CancelFunc)
	sealed := make(map[string]time.Time)

	stats := NewAgentStatsTracker()
	unsubscribe := cc.Agent.Observe(coreevents.ObserverFunc(func(event *aop.Event) {
		if next, changed := stats.Observe(event); changed {
			send("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentStats{AgentStats: next}})
		}
		replyTo := ""
		isArtifact := false
		if event.GetToolResult() != nil {
			replyTo = event.GetToolResult().GetCallId()
		} else if extension := event.GetExtension(); extension != nil {
			artifact := new(toolpb.Artifact)
			isArtifact = extension.MessageIs(artifact)
			correlation := new(operationpb.Ref)
			if found, err := aop.FindTypedExtension(event, correlation); err == nil && found {
				replyTo = correlation.GetCallId()
			}
		}
		// A streaming tool (katana) keeps emitting artifacts from background
		// workers after its terminal has been sent, and keeps crawling after its
		// call was canceled. Each such trailing artifact earns an "after terminal
		// barrier" rejection on the control plane; at scale that floods a server
		// core and the logs. Drop them at the source once the call is sealed.
		// Only ids this connection sealed are dropped: artifacts from calls it
		// never dispatched (the node's own agent loop, standalone scans) carry
		// call ids it has never seen and must still reach the hub.
		if isArtifact && replyTo != "" && callIsSealed(&operationsMu, sealed, replyTo) {
			return
		}
		send(replyTo, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: event}})
	}))
	if unsubscribe == nil {
		return fmt.Errorf("agent event subscription is required")
	}
	defer unsubscribe.Cancel()
	if detach := attachToolProgress(cc.Progress, send); detach != nil {
		defer detach.Close(context.Background())
	}
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

	sendEnvelope := func(envelope *aop.Envelope) {
		if envelope == nil {
			return
		}
		select {
		case sendCh <- envelope:
		case <-connectionCtx.Done():
		}
	}
	namespaceMux, err := newAgentConnectionNamespaceMux(connectionCtx, cc, send, &operationsMu, operations, sealed)
	if err != nil {
		return fmt.Errorf("register connection namespaces: %w", err)
	}
	defer func() {
		cancelConnection()
		_ = namespaceMux.Close(context.Background())
	}()
	// The existing connection owns IO and cancellation. Register the same
	// business handlers as embedded Host without another connection wrapper.
	reply := func(envelope *aop.Envelope) error {
		sendEnvelope(envelope)
		return connectionCtx.Err()
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
		intercepted, err := interceptCancelOperation(envelope, &operationsMu, operations, sealed)
		if err != nil {
			send(envelope.GetId(), protocolFailure("INVALID_PAYLOAD", err.Error()))
			continue
		}
		if intercepted {
			continue
		}
		handled, err := namespaceMux.Dispatch(envelope, reply)
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
	return protobuf.CloneOf(value)
}

func newAgentConnectionNamespaceMux(
	connectionCtx context.Context,
	cc connectionConfig,
	send func(string, protobuf.Message),
	operationsMu *sync.Mutex,
	operations map[string]context.CancelFunc,
	sealed map[string]time.Time,
) (*aop.NamespaceMux, error) {
	mux := aop.NewNamespaceMux(connectionCtx)
	ok := false
	defer func() {
		if !ok {
			_ = mux.Close(context.Background())
		}
	}()
	if err := mux.Register(&toolpb.ProtocolMessage{}, func(ctx context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, ok := message.(*toolpb.ProtocolMessage)
		if !ok {
			return fmt.Errorf("unexpected tool namespace message %T", message)
		}
		handleAgentToolMessage(ctx, cc, envelope, value, send, operationsMu, operations, sealed)
		return nil
	}); err != nil {
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
	if err := mux.Register(&execpb.ProtocolMessage{}, func(ctx context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, ok := message.(*execpb.ProtocolMessage)
		if !ok {
			return fmt.Errorf("unexpected exec namespace message %T", message)
		}
		handleAgentExecMessage(ctx, cc, envelope, value, send, operationsMu, operations)
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

// interceptCancelOperation handles connection-local call cancellation before
// typed namespace dispatch. Session control remains owned by its contributed
// aop.ProtocolMessage binding, so a connection never installs a second handler
// for the same protobuf namespace.
func interceptCancelOperation(
	envelope *aop.Envelope,
	operationsMu *sync.Mutex,
	operations map[string]context.CancelFunc,
	sealed map[string]time.Time,
) (bool, error) {
	prototype := &aop.ProtocolMessage{}
	if envelope == nil || envelope.Payload == nil || !envelope.Payload.MessageIs(prototype) {
		return false, nil
	}
	canonical := "type.googleapis.com/" + string(prototype.ProtoReflect().Descriptor().FullName())
	if envelope.Payload.TypeUrl != canonical {
		return false, fmt.Errorf("non-canonical type URL %q, want %q", envelope.Payload.TypeUrl, canonical)
	}
	value := new(aop.ProtocolMessage)
	if err := envelope.Payload.UnmarshalTo(value); err != nil {
		return false, fmt.Errorf("decode core protocol message: %w", err)
	}
	payload, ok := value.Message.(*aop.ProtocolMessage_CancelOperation)
	if !ok {
		return false, nil
	}
	targetID := payload.CancelOperation.GetTargetId()
	operationsMu.Lock()
	cancel := operations[targetID]
	operationsMu.Unlock()
	if cancel == nil {
		return true, nil
	}
	// Cancellation is advisory: a scanner that ignores its context (katana's
	// Crawl takes no ctx) keeps running and emitting for the rest of its crawl.
	// Seal the call before cancellation so trailing artifacts stay off the wire.
	sealCall(operationsMu, sealed, targetID)
	cancel()
	return true, nil
}

func handleAgentToolMessage(ctx context.Context, cc connectionConfig, envelope *aop.Envelope, value *toolpb.ProtocolMessage, send func(string, protobuf.Message), operationsMu *sync.Mutex, operations map[string]context.CancelFunc, sealed map[string]time.Time) {
	replyTo := envelope.GetId()
	fail := func(message string) { send(replyTo, protocolFailure("OPERATION_FAILED", message)) }
	request := value.GetCall()
	if request == nil || request.Call == nil {
		fail("unsupported AOP tool message")
		return
	}
	operationID := envelope.GetId()
	if request.Call.Id == "" {
		request.Call.Id = operationID
	}
	// The hub is the canonical publisher for a remotely dispatched tool.call.
	// This node only publishes the terminal result; synthesizing the call here
	// would duplicate the hub's session timeline entry.
	taskCtx, taskCancel := context.WithCancel(ctx)
	trackOperation(operationsMu, operations, operationID, taskCancel)
	// seal closes this call's artifact window so the forwarding subscriber drops
	// anything a streaming tool emits from here on. It must run before the
	// terminal is sent: the terminal is the last message a call may put on the
	// wire, and a trailing artifact forwarded after it is rejected by the control
	// plane. Everything emitted before the seal is already queued ahead of the
	// terminal on the FIFO send channel. This narrows the tail to nothing rather
	// than closing it absolutely: an artifact that passed the seal check may
	// still be queued just after the pty. Forwarding the whole tail is what
	// floods the control plane; a stray record is what it counts and drops.
	seal := func() { sealCall(operationsMu, sealed, operationID) }
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				cc.Logger.Errorf("tool operation panic operation_id=%s tool=%s panic=%v\n%s", operationID, request.Call.Name, recovered, debug.Stack())
				fail("tool operation failed unexpectedly")
			}
		}()
		defer finishOperation(operationsMu, operations, operationID, taskCancel)
		taskCtx = operation.ContextWithInvocation(taskCtx, operation.Invocation{Emitter: cc.Name})
		executor := connectionExecutor(cc)
		if executor == nil {
			seal()
			fail("tool executor is unavailable")
			return
		}
		event, err := toolset.ExecuteToolRequest(taskCtx, operationID, request, executor, cc.Progress)
		if err != nil {
			seal()
			fail(err.Error())
			return
		}
		// The endpoint is the single event source for the connection. Its
		// subscriber forwards the terminal to the wire; do not send a second copy.
		seal()
		cc.Agent.Publish(event)
	}()
}

func connectionExecutor(cc connectionConfig) tool.Executor {
	if cc.Executor != nil {
		return cc.Executor
	}
	return tool.EmptyExecutor()
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

// sealedCallRetention bounds the sealed set. Trailing artifacts follow their
// call within seconds — a scanner still emitting minutes after the hub gave up
// has bigger problems than one forwarded record — so tombstones are pruned on
// the next seal rather than kept for the life of the connection.
const sealedCallRetention = time.Minute

// sealCall marks a call as no longer allowed to put artifacts on the wire.
func sealCall(mu *sync.Mutex, sealed map[string]time.Time, id string) {
	if id == "" {
		return
	}
	now := time.Now()
	mu.Lock()
	for other, at := range sealed {
		if now.Sub(at) > sealedCallRetention {
			delete(sealed, other)
		}
	}
	sealed[id] = now
	mu.Unlock()
}

// callIsSealed reports whether this connection has closed the call's artifact
// window. Ids it never sealed — including calls it never dispatched, whose
// artifacts come from the node's own agent loop or a standalone scan — are not
// sealed and keep flowing.
func callIsSealed(mu *sync.Mutex, sealed map[string]time.Time, id string) bool {
	mu.Lock()
	_, done := sealed[id]
	mu.Unlock()
	return done
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
