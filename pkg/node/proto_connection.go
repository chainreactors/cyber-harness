package node

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/chainreactors/cyber/agent"
	aop "github.com/chainreactors/cyber/aop"
	filepb "github.com/chainreactors/cyber/aop/file"
	toolpb "github.com/chainreactors/cyber/aop/tool"
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
	dialURL, accessKey, err := aopws.DialURL(cc.ServerURL, toolnode.DefaultWSPath)
	if err != nil {
		return nil, err
	}
	var headers http.Header
	if accessKey != "" {
		headers = http.Header{"Authorization": {"Bearer " + accessKey}}
	}
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, dialURL, headers)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		if response != nil {
			switch response.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				return nil, fmt.Errorf("WebSocket authentication rejected (HTTP %d): check the runner token: %w", response.StatusCode, err)
			case http.StatusNotFound:
				return nil, fmt.Errorf("WebSocket endpoint not found (HTTP %d): check the server URL and WebSocket path: %w", response.StatusCode, err)
			default:
				return nil, fmt.Errorf("WebSocket handshake rejected (HTTP %d): %w", response.StatusCode, err)
			}
		}
		return nil, err
	}
	return aopws.New(ctx, conn, aopws.Options{
		Encoding:     aopws.Binary,
		WriteTimeout: websocketWriteWait,
		PingInterval: websocketPingPeriod,
		PongTimeout:  websocketPongWait,
	})
}

func shouldResetReconnectBackoff(connectedAt, disconnectedAt time.Time) bool {
	return !connectedAt.IsZero() && disconnectedAt.Sub(connectedAt) >= reconnectStableAfter
}

func connect(ctx context.Context, cc connectionConfig) error {
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

func serveAgentConnection(ctx context.Context, cc connectionConfig, logger telemetry.Logger, stream aop.EnvelopeStream) error {
	if cc.Events == nil {
		return fmt.Errorf("agent event stream is required")
	}
	executor := cc.Executor
	if executor == nil {
		executor = coretool.EmptyExecutor()
	}
	hello, err := BuildHello(cc.Name, executor, cc.NodeID, cc.Runtime)
	if err != nil {
		return err
	}
	helloEnvelope := aop.Reply("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentHello{AgentHello: hello}})
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
	calls := &toolnode.CallHandler{Executor: executor, Progress: cc.Progress, Emitter: cc.Name, Logger: logger, Send: send, Publish: cc.Events.Publish}
	namespaceMux := aop.NewNamespaceMux(connectionCtx)
	defer func() { namespaceMux.Cancel(); _ = namespaceMux.Close(context.Background()) }()
	if err := namespaceMux.Register(&toolpb.ProtocolMessage{}, calls.Handle); err != nil {
		return err
	}
	if err := namespaceMux.Register(&filepb.ProtocolMessage{}, func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, ok := message.(*filepb.ProtocolMessage)
		if !ok {
			return fmt.Errorf("unexpected file namespace message %T", message)
		}
		handleAgentFileMessage(cc, envelope, value, send)
		return nil
	}); err != nil {
		return err
	}
	if err := namespaceMux.Register(&types.ReloadProtocolMessage{}, func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, ok := message.(*types.ReloadProtocolMessage)
		if !ok {
			return fmt.Errorf("unexpected reload namespace message %T", message)
		}
		handleAgentReloadMessage(cc, envelope, value, send)
		return nil
	}); err != nil {
		return err
	}
	if cc.RegisterNamespaces != nil {
		if err := cc.RegisterNamespaces(namespaceMux); err != nil {
			return err
		}
	}
	stats := NewAgentStatsTracker()
	unsubscribe := cc.Events.Observe(func(event *aop.Event) {
		if next, changed := stats.Observe(event); changed {
			send("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentStats{AgentStats: next}})
		}
		calls.Forward(event)
	})
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
			send(envelope.GetId(), aop.NewProtocolError("INVALID_PAYLOAD", err.Error()))
		} else if !handled {
			send(envelope.GetId(), aop.NewProtocolError("UNSUPPORTED_NAMESPACE", "unsupported AOP namespace"))
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

func handleAgentFileMessage(cc connectionConfig, envelope *aop.Envelope, value *filepb.ProtocolMessage, send func(string, protobuf.Message)) {
	replyTo := envelope.GetId()
	fail := func(message string) { send(replyTo, aop.NewProtocolError("OPERATION_FAILED", message)) }
	switch payload := value.Message.(type) {
	case *filepb.ProtocolMessage_ReadRequest, *filepb.ProtocolMessage_WriteRequest,
		*filepb.ProtocolMessage_ListRequest, *filepb.ProtocolMessage_MkdirRequest:
		fail("file operation is unavailable")
	case *filepb.ProtocolMessage_UploadRequest:
		go func() {
			if cc.Upload == nil {
				fail("upload handler is unavailable")
				return
			}
			result, err := cc.Upload(payload.UploadRequest)
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
	fail := func(message string) { send(replyTo, aop.NewProtocolError("OPERATION_FAILED", message)) }
	request := value.GetRequest()
	if request == nil || request.Config == nil || cc.ReloadConfig == nil {
		fail("config reload request is unavailable")
		return
	}
	result, status := cc.ReloadConfig(request.Config)
	if status != nil {
		send("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentStatus{AgentStatus: status}})
	}
	send(replyTo, &types.ReloadProtocolMessage{Message: &types.ReloadProtocolMessage_Result{Result: result}})
	if result.Ok && cc.CommitReload != nil {
		cc.CommitReload()
	}
}
