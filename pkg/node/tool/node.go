// Package toolnode is the outbound AOP tool node: it connects an Agent-free
// tool.Executor to a hub and serves tool calls until the context ends.
package toolnode

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"runtime"
	"strings"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/eventbus"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/pkg/aopconn"
	"github.com/chainreactors/cyber/pkg/aopws"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// DefaultWSPath is the hub route the tool node dials; the agent node in pkg/node
// dials the same route.
const (
	DefaultWSPath        = "/api/aop/node/ws"
	websocketWriteWait   = 10 * time.Second
	websocketPingPeriod  = 30 * time.Second
	websocketPongTimeout = 90 * time.Second
)

// Identity identifies the process and one accepted connection.
type Identity struct {
	InstanceID string
	Nonce      string
}

// NamespaceSession owns connection-local work, never host resources.
type NamespaceSession interface {
	Cancel(string)
	Close(context.Context) error
}

type Config struct {
	Metadata  map[string]any
	ServerURL string
	WSPath    string
	ID        string
	Token     string
	Version   string
	JSON      bool
	Executor  coretool.Executor
	Events    *coreevents.Stream
	Progress  *eventbus.Bus[*toolpb.Progress]
	// RegisterNamespaces installs resource-control protocols on each new
	// connection. The profile-owned extensions remain loaded across reconnects;
	// the connection-owned mux only owns registration admission and draining.
	RegisterNamespaces func(*aop.NamespaceMux) error
	// OpenNamespaces runs after AgentAccepted and before inbound dispatch.
	OpenNamespaces func(*aop.NamespaceMux, *CallHandler, Identity) (NamespaceSession, error)
	// ConnectionMetadata adds identity-bound values to AgentHello.
	ConnectionMetadata func(Identity) map[string]any
	// ShouldRetry can reject reconnection after a terminal connection error.
	ShouldRetry func(error) bool
	Logger      telemetry.Logger
	Dialer      *websocket.Dialer
}

// Run connects an Agent-free tool.Executor to the hub and serves until ctx
// ends. The external framework owns reasoning, history, retries and scheduling.
// One stable process instance ID is reused across reconnects; accepted calls are
// canceled and drained before reconnecting.
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
	if cfg.Events == nil {
		cfg.Events = coreevents.New()
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
		if cfg.ShouldRetry != nil && !cfg.ShouldRetry(err) {
			return err
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

// EnrollmentError carries the hub's rejection code for retry policy decisions.
type EnrollmentError struct{ Code, Message string }

func (e *EnrollmentError) Error() string {
	return fmt.Sprintf("AOP enrollment rejected: %s: %s", e.Code, e.Message)
}

func runConnection(ctx context.Context, cfg Config, instanceID string) error {
	dialURL, token, err := aopws.DialURL(cfg.ServerURL, cfg.WSPath)
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
	connectionCtx, cancel := context.WithCancel(ctx)
	encoding := aopws.Binary
	if cfg.JSON {
		encoding = aopws.ProtoJSON
	}
	stream, err := aopws.New(connectionCtx, conn, aopws.Options{
		Encoding:     encoding,
		WriteTimeout: websocketWriteWait,
		PingInterval: websocketPingPeriod,
		PongTimeout:  websocketPongTimeout,
	})
	if err != nil {
		cancel()
		return err
	}
	defer func() {
		cancel()
		_ = stream.Close()
	}()
	namespaces := aop.NewNamespaceMux(connectionCtx)
	defer func() { _ = namespaces.Close(context.Background()) }()
	identity := Identity{InstanceID: instanceID, Nonce: rand.Text()}
	if cfg.RegisterNamespaces != nil {
		if err := cfg.RegisterNamespaces(namespaces); err != nil {
			return fmt.Errorf("register tool node namespaces: %w", err)
		}
	}
	hello, err := hello(cfg, identity)
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
	message, err := aop.Unwrap(acceptedEnvelope)
	if err != nil {
		return err
	}
	accepted, ok := message.(*aop.ProtocolMessage)
	if !ok || acceptedEnvelope.ReplyTo != helloEnvelope.Id {
		return fmt.Errorf("expected AOP enrollment response")
	}
	if rejected := accepted.GetProtocolError(); rejected != nil {
		return &EnrollmentError{Code: rejected.GetCode(), Message: rejected.GetMessage()}
	}
	if accepted.GetAgentAccepted() == nil {
		return fmt.Errorf("expected AOP agent acceptance")
	}
	connection, err := aopconn.NewConnection(connectionCtx, stream)
	if err != nil {
		return err
	}
	defer connection.Close()
	// The same connection context ends work when either reader or writer fails.
	stop := context.AfterFunc(connection.Context(), cancel)
	defer stop()
	send := func(id string, message proto.Message) { _ = connection.Send(aop.Reply(id, message)) }
	calls := &CallHandler{Executor: cfg.Executor, Progress: cfg.Progress, Emitter: cfg.ID, Logger: cfg.Logger, Send: send, Publish: cfg.Events.Publish}
	var namespaceSession NamespaceSession
	var unsubscribe []func()
	defer func() {
		cancel()
		calls.Close()
		for _, stop := range unsubscribe {
			stop()
		}
		if namespaceSession != nil {
			_ = namespaceSession.Close(context.Background())
		}
	}()
	if cfg.OpenNamespaces != nil {
		namespaceSession, err = cfg.OpenNamespaces(namespaces, calls, identity)
		if err != nil {
			return fmt.Errorf("open tool node namespaces: %w", err)
		}
	}
	if err := namespaces.Register(&toolpb.ProtocolMessage{}, calls.Handle); err != nil {
		return err
	}
	if cfg.Progress != nil {
		sub := cfg.Progress.Subscribe(calls.ForwardProgress)
		unsubscribe = append(unsubscribe, sub.Cancel)
	}
	sub := cfg.Events.Observe(calls.Forward)
	unsubscribe = append(unsubscribe, sub.Cancel)
	return connection.Run(func(_ context.Context, envelope *aop.Envelope, reply aop.SendFunc) error {
		if target, canceling := cancelOperationTarget(envelope); canceling {
			calls.Cancel(envelope)
			if namespaceSession != nil {
				namespaceSession.Cancel(target)
			}
			return nil
		}
		handled, err := namespaces.Dispatch(envelope, reply)
		if err != nil {
			send(envelope.GetId(), aop.NewProtocolError("INVALID_PAYLOAD", err.Error()))
		} else if !handled {
			send(envelope.GetId(), aop.NewProtocolError("UNSUPPORTED_NAMESPACE", "unsupported AOP namespace"))
		}
		return nil
	})
}

func cancelOperationTarget(envelope *aop.Envelope) (string, bool) {
	if envelope == nil || envelope.Payload == nil || !envelope.Payload.MessageIs(&aop.ProtocolMessage{}) {
		return "", false
	}
	value := new(aop.ProtocolMessage)
	if envelope.Payload.UnmarshalTo(value) != nil || value.GetCancelOperation() == nil {
		return "", false
	}
	return value.GetCancelOperation().GetTargetId(), true
}

func hello(cfg Config, identity Identity) (*aop.AgentHello, error) {
	hostname, _ := os.Hostname()
	workDir, _ := os.Getwd()
	username := ""
	if current, err := user.Current(); err == nil {
		username = current.Username
	}
	values := make(map[string]any, len(cfg.Metadata)+3)
	for key, value := range cfg.Metadata {
		values[key] = value
	}
	if cfg.ConnectionMetadata != nil {
		for key, value := range cfg.ConnectionMetadata(identity) {
			values[key] = value
		}
	}
	values["version"], values["mode"], values["instance_id"] = cfg.Version, "tool", identity.InstanceID
	metadata, err := structpb.NewStruct(values)
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
