// Package toolnode connects an Agent-free tool.Executor to an AOP WebSocket
// hub. The external framework owns reasoning, history, retries, and scheduling.
package toolnode

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"runtime"
	"strings"
	"sync"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/eventbus"
	coreevents "github.com/chainreactors/aiscan/core/events"
	"github.com/chainreactors/aiscan/core/operation"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/toolset"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

const DefaultWSPath = "/api/aop/node/ws"

type Config struct {
	ServerURL string
	WSPath    string
	ID        string
	Token     string
	Version   string
	JSON      bool
	Executor  tool.Executor
	Events    *coreevents.Stream
	Progress  *eventbus.Bus[*toolpb.Progress]
	// RegisterNamespaces installs resource-control protocols on each new
	// connection. The profile-owned extensions remain loaded across reconnects;
	// the connection-owned mux only owns registration admission and draining.
	RegisterNamespaces func(*aop.NamespaceMux) error
	Logger             telemetry.Logger
	Dialer             *websocket.Dialer
}

// Run serves until ctx ends. One stable process instance ID is reused across
// reconnects; accepted calls are canceled and drained before reconnecting.
func Run(ctx context.Context, cfg Config) error {
	if ctx == nil {
		return fmt.Errorf("tool node context is required")
	}
	if cfg.Executor == nil {
		return fmt.Errorf("tool node executor is required")
	}
	if strings.TrimSpace(cfg.ServerURL) == "" {
		return fmt.Errorf("tool node server URL is required")
	}
	if cfg.WSPath == "" {
		cfg.WSPath = DefaultWSPath
	}
	if cfg.ID == "" {
		cfg.ID, _ = os.Hostname()
	}
	if cfg.ID == "" {
		return fmt.Errorf("tool node ID is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = telemetry.NopLogger()
	}
	if cfg.Dialer == nil {
		cfg.Dialer = websocket.DefaultDialer
	}
	instanceID := aop.EnvelopeID()
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := runConnection(ctx, cfg, instanceID)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		delay := retryDelay(attempt)
		cfg.Logger.Warnf("tool node connection lost, retrying in %s: %v", delay, err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func retryDelay(attempt int) time.Duration {
	if attempt > 5 {
		attempt = 5
	}
	return time.Duration(1<<attempt) * 250 * time.Millisecond
}

type stream struct {
	conn *websocket.Conn
	json bool
}

func (s stream) recv() (*aop.Envelope, error) {
	_, data, err := s.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	envelope := new(aop.Envelope)
	if s.json {
		err = protojson.Unmarshal(data, envelope)
	} else {
		err = proto.Unmarshal(data, envelope)
	}
	return envelope, err
}

func (s stream) send(envelope *aop.Envelope) error {
	frame := websocket.BinaryMessage
	var data []byte
	var err error
	if s.json {
		frame = websocket.TextMessage
		data, err = protojson.Marshal(envelope)
	} else {
		data, err = proto.Marshal(envelope)
	}
	if err != nil {
		return err
	}
	_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.conn.WriteMessage(frame, data)
}

func runConnection(ctx context.Context, cfg Config, instanceID string) error {
	dialURL, token, err := connectionURL(cfg.ServerURL, cfg.WSPath)
	if err != nil {
		return err
	}
	if cfg.Token != "" {
		token = cfg.Token
	}
	headers := http.Header{}
	if token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	conn, response, err := cfg.Dialer.DialContext(ctx, dialURL, headers)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		return err
	}
	s := stream{conn: conn, json: cfg.JSON}
	connectionCtx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		_ = conn.Close()
	}()
	go func() {
		<-connectionCtx.Done()
		_ = conn.Close()
	}()
	namespaces := aop.NewNamespaceMux(connectionCtx)
	if cfg.RegisterNamespaces != nil {
		if err := cfg.RegisterNamespaces(namespaces); err != nil {
			return fmt.Errorf("register tool node namespaces: %w", err)
		}
	}
	defer func() { _ = namespaces.Close(context.Background()) }()

	hello, err := hello(cfg, instanceID)
	if err != nil {
		return err
	}
	helloEnvelope := aop.Reply("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentHello{AgentHello: hello}})
	if err := s.send(helloEnvelope); err != nil {
		return err
	}
	acceptedEnvelope, err := s.recv()
	if err != nil {
		return err
	}
	message, err := aop.Unwrap(acceptedEnvelope)
	if err != nil {
		return err
	}
	accepted, ok := message.(*aop.ProtocolMessage)
	if !ok || acceptedEnvelope.ReplyTo != helloEnvelope.Id {
		return fmt.Errorf("expected AOP enrollment response")
	}
	if rejected := accepted.GetProtocolError(); rejected != nil {
		return fmt.Errorf("AOP enrollment rejected: %s: %s", rejected.GetCode(), rejected.GetMessage())
	}
	if accepted.GetAgentAccepted() == nil {
		return fmt.Errorf("expected AOP agent acceptance")
	}
	sendCh := make(chan *aop.Envelope, 64)
	writerErr := make(chan error, 1)
	go func() {
		for {
			select {
			case envelope := <-sendCh:
				if err := s.send(envelope); err != nil {
					select {
					case writerErr <- err:
					default:
					}
					cancel()
					return
				}
			case <-connectionCtx.Done():
				return
			}
		}
	}()
	send := func(replyTo string, value proto.Message) {
		envelope := aop.Reply(replyTo, value)
		select {
		case sendCh <- envelope:
		case <-connectionCtx.Done():
		}
	}

	var operations sync.Map
	var wg sync.WaitGroup
	if cfg.Progress != nil {
		unsubscribe := cfg.Progress.Subscribe(func(progress *toolpb.Progress) {
			if progress == nil || strings.TrimSpace(progress.GetText()) == "" {
				return
			}
			if callID := progress.GetCallId(); callID != "" {
				if _, active := operations.Load(callID); !active {
					return
				}
			}
			copy := proto.Clone(progress).(*toolpb.Progress)
			copy.Text = strings.ToValidUTF8(copy.Text, "\uFFFD")
			send(copy.GetCallId(), &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Progress{Progress: copy}})
		})
		defer unsubscribe.Cancel()
	}
	if cfg.Events != nil {
		unsubscribe := cfg.Events.Subscribe(func(event *aop.Event) {
			if event == nil {
				return
			}
			// Every AOP event uses the same ordered connection lane. Synchronous
			// observations emitted during a call therefore precede its terminal
			// result, while genuinely detached observations remain valid later.
			send("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: proto.Clone(event).(*aop.Event)}})
		})
		defer unsubscribe.Cancel()
	}
	defer func() {
		cancel()
		operations.Range(func(_, value any) bool {
			value.(context.CancelFunc)()
			return true
		})
		wg.Wait()
	}()

	for {
		envelope, err := s.recv()
		if err != nil {
			select {
			case writeErr := <-writerErr:
				return writeErr
			default:
			}
			return err
		}
		value, err := aop.Unwrap(envelope)
		if err != nil {
			send(envelope.GetId(), aop.NewProtocolError("INVALID_PAYLOAD", err.Error()))
			continue
		}
		switch message := value.(type) {
		case *aop.ProtocolMessage:
			cancelRequest := message.GetCancelOperation()
			if cancelRequest == nil {
				send(envelope.GetId(), aop.NewProtocolError("UNSUPPORTED_MESSAGE", "tool node only accepts cancellation in the core namespace"))
				continue
			}
			if cancelCall, ok := operations.Load(cancelRequest.GetTargetId()); ok {
				cancelCall.(context.CancelFunc)()
			}
		case *toolpb.ProtocolMessage:
			request := message.GetCall()
			if request == nil || request.Call == nil {
				send(envelope.GetId(), aop.NewProtocolError("INVALID_PAYLOAD", "tool call is required"))
				continue
			}
			operationID := envelope.GetId()
			if operationID == "" || request.Call.Id != "" && request.Call.Id != operationID {
				send(operationID, aop.NewProtocolError("INVALID_PAYLOAD", "tool call ID must match envelope ID"))
				continue
			}
			callCtx, callCancel := context.WithCancel(connectionCtx)
			if _, loaded := operations.LoadOrStore(operationID, callCancel); loaded {
				callCancel()
				send(operationID, aop.NewProtocolError("DUPLICATE_OPERATION", "operation ID is already active"))
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer operations.Delete(operationID)
				defer callCancel()
				execute(callCtx, cfg, operationID, request, send)
			}()
		default:
			handled, dispatchErr := namespaces.Dispatch(envelope, func(reply *aop.Envelope) error {
				select {
				case sendCh <- reply:
					return nil
				case <-connectionCtx.Done():
					return connectionCtx.Err()
				}
			})
			if dispatchErr != nil {
				send(envelope.GetId(), aop.NewProtocolError("INVALID_PAYLOAD", dispatchErr.Error()))
				continue
			}
			if !handled {
				send(envelope.GetId(), aop.NewProtocolError("UNSUPPORTED_NAMESPACE", "unsupported AOP namespace"))
			}
		}
	}
}

func execute(ctx context.Context, cfg Config, operationID string, request *toolpb.Call, send func(string, proto.Message)) {
	call := request.Call
	if call.Id == "" {
		call.Id = operationID
	}
	invocation := operation.Invocation{
		WorkDir: call.WorkingDirectory, CallID: operationID,
		SessionID: request.SessionId, TurnID: request.TurnId, Emitter: cfg.ID,
	}
	event, err := toolset.ExecuteToolRequest(operation.ContextWithInvocation(ctx, invocation), operationID, request, cfg.Executor, cfg.Progress)
	if err != nil {
		send(operationID, aop.NewProtocolError("INVALID_PAYLOAD", err.Error()))
		return
	}
	send(operationID, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: event}})
}

func hello(cfg Config, instanceID string) (*aop.AgentHello, error) {
	hostname, _ := os.Hostname()
	workDir, _ := os.Getwd()
	username := ""
	if current, err := user.Current(); err == nil {
		username = current.Username
	}
	metadata, err := structpb.NewStruct(map[string]any{
		"version": cfg.Version, "mode": "tool", "instance_id": instanceID,
	})
	if err != nil {
		return nil, err
	}
	return &aop.AgentHello{
		NodeId: cfg.ID, Name: cfg.ID, Capabilities: []string{"tool"},
		Tools: cfg.Executor.ToolDefinitions(),
		Runtime: &aop.AgentRuntimeInfo{
			Hostname: hostname, Username: username, WorkingDir: workDir,
			Os: runtime.GOOS, Arch: runtime.GOARCH, Pid: int32(os.Getpid()), Metadata: metadata,
		},
	}, nil
}

func connectionURL(serverURL, path string) (string, string, error) {
	u, err := url.Parse(strings.TrimRight(serverURL, "/"))
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("invalid tool node server URL %q", serverURL)
	}
	token := ""
	if u.User != nil {
		token = u.User.Username()
		u.User = nil
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", "", fmt.Errorf("unsupported tool node URL scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(path, "/")
	return u.String(), token, nil
}
