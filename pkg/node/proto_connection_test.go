package node

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	promptext "github.com/chainreactors/cyber/pkg/exts/prompt"

	agentsession "github.com/chainreactors/cyber/agent/session"
	aop "github.com/chainreactors/cyber/aop"
	filepb "github.com/chainreactors/cyber/aop/file"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	trafficpb "github.com/chainreactors/cyber/aop/traffic"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/namespaces"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	types "github.com/chainreactors/cyber/core/types"
	"github.com/chainreactors/cyber/internal/testutil/apptest"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	"github.com/chainreactors/cyber/pkg/aopws"

	proxyext "github.com/chainreactors/cyber/pkg/exts/proxy"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	toolnode "github.com/chainreactors/cyber/pkg/node/tool"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
)

type singleDeliveryProbeTool struct{}

var testUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func testToolExecutor(t *testing.T, tools ...coretool.Tool) coretool.Executor {
	t.Helper()
	return hosttest.Tools(t, tools...)
}

func (singleDeliveryProbeTool) Name() string { return "single_delivery_probe" }

func (singleDeliveryProbeTool) Description() string { return "test tool" }

func (singleDeliveryProbeTool) Definition() *aop.ToolDefinition {
	return coretool.Def("single_delivery_probe", "test tool", struct{}{})
}

func (singleDeliveryProbeTool) Execute(context.Context, string) (*coretool.Result, error) {
	return coretool.TextResult("probe result"), nil
}

type handshakeThenEOFStream struct {
	helloID   string
	recvs     int
	eventSent chan struct{}
}

func (s *handshakeThenEOFStream) Send(envelope *aop.Envelope) error {
	if s.helloID == "" {
		s.helloID = envelope.GetId()
	}
	if s.eventSent != nil {
		message, err := aop.Unwrap(envelope)
		if err == nil {
			if value, ok := message.(*aop.ProtocolMessage); ok && value.GetEvent() != nil {
				close(s.eventSent)
			}
		}
	}
	return nil
}

func (s *handshakeThenEOFStream) Recv() (*aop.Envelope, error) {
	s.recvs++
	if s.recvs == 1 {
		return aop.MustWrap("accepted", s.helloID, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{NodeId: "runner-1"}}}), nil
	}
	if s.eventSent != nil {
		select {
		case <-s.eventSent:
		case <-time.After(time.Second):
			return nil, errors.New("event published with command catalog was not forwarded")
		}
	}
	return nil, io.EOF
}

func TestServeAgentConnectionSubscribesBeforePublishingMenu(t *testing.T) {
	stream := &handshakeThenEOFStream{eventSent: make(chan struct{})}
	menuCalled := false
	events := coreevents.New()
	cc := connectionConfig{
		Name:   "runner-1",
		NodeID: "runner-1",
		Events: events,
		Menu: func() []*types.CommandSpec {
			menuCalled = true
			events.Publish(&aop.Event{SessionId: "test"})
			return nil
		},
	}
	if err := serveAgentConnection(context.Background(), cc, telemetry.NopLogger(), stream); err != io.EOF {
		t.Fatalf("serveAgentConnection error = %v, want EOF", err)
	}
	if !menuCalled {
		t.Fatal("command catalog was not published")
	}
}

func TestToolOperationPanicIsReportedAndCleanedUp(t *testing.T) {
	var logs bytes.Buffer
	logger := telemetry.NewLogger(telemetry.LogConfig{Debug: true, Output: &logs})
	failure := make(chan *aop.ProtocolError, 1)
	send := func(_ string, message protobuf.Message) {
		protocol := message.(*aop.ProtocolMessage)
		if protocol.GetEvent() != nil {
			panic("send event boom")
		}
		if value := protocol.GetProtocolError(); value != nil {
			failure <- value
		}
	}
	arguments, _ := aop.JSONValue(map[string]any{})
	request := &toolpb.Call{Call: &aop.ToolCall{Id: "op-panic", Name: "missing", Arguments: arguments}}
	handler := &toolnode.CallHandler{Executor: coretool.EmptyExecutor(), Logger: logger, Publish: func(*aop.Event) { panic("send event boom") }, Send: send}
	handler.Handle(
		context.Background(),
		&aop.Envelope{Id: "op-panic"},
		&toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Call{Call: request}},
		nil,
	)

	select {
	case got := <-failure:
		if got.Code != "OPERATION_FAILED" || !strings.Contains(got.Message, "unexpectedly") {
			t.Fatalf("failure = %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for operation failure")
	}
	handler.Close()
	if got := logs.String(); !strings.Contains(got, "send event boom") || !strings.Contains(got, "op-panic") {
		t.Fatalf("panic log = %s", got)
	}
}

// Canceling a call does not stop a scanner that ignores its context, so the
// hub having given up must also close that call's artifact window — otherwise
// the rest of the crawl crosses the wire only to be rejected on arrival.
func TestManagerToolResultUsesSingleDeliveryPath(t *testing.T) {
	ctx := context.Background()
	app := apptest.NewFixture(t, telemetry.NopLogger(), nil)

	rt := sessionext.New(agentsession.Config{Logger: telemetry.NopLogger()})
	ns := namespaces.New()
	rtSet := hosttest.Set(t, append(apptest.Entries(t, app), ns, promptext.New(), loopext.New(agent.StandardLoop{}), rt, sessionext.NewProtocol())...)
	if err := rtSet.Load(ctx); err != nil {
		t.Fatal(err)
	}
	defer rtSet.Close(context.Background())

	registry := testToolExecutor(t, singleDeliveryProbeTool{})
	runtimeEvents := make(chan *aop.Event, 1)
	var runtimeToolCalls atomic.Int32
	unsubscribe := rt.Runtime().Observe(func(event *aop.Event) {
		if event == nil {
			return
		}
		if event.GetToolCall() != nil {
			runtimeToolCalls.Add(1)
		}
		if event.GetToolResult() != nil {
			runtimeEvents <- event
		}
	})
	defer unsubscribe.Cancel()
	directMessages := make(chan protobuf.Message, 2)
	send := func(_ string, message protobuf.Message) { directMessages <- message }
	arguments, err := aop.JSONValue(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	request := &toolpb.Call{Call: &aop.ToolCall{
		Id:        "single-delivery-op",
		Name:      "single_delivery_probe",
		Arguments: arguments,
	}}
	handler := &toolnode.CallHandler{Executor: registry, Logger: telemetry.NopLogger(), Publish: rt.Runtime().Publish, Send: send}
	defer handler.Close()
	if err := handler.Handle(ctx, &aop.Envelope{Id: "single-delivery-op"}, &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Call{Call: request}}, nil); err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-runtimeEvents:
		if got := event.GetToolResult().GetName(); got != "single_delivery_probe" {
			t.Fatalf("runtime tool result name = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for runtime tool result")
	}
	select {
	case message := <-directMessages:
		t.Fatalf("tool result was sent directly in addition to runtime event: %T", message)
	case <-time.After(100 * time.Millisecond):
	}
	if got := runtimeToolCalls.Load(); got != 0 {
		t.Fatalf("remote tool request unexpectedly emitted %d tool.call events; the hub is the canonical source", got)
	}
}

func TestAgentHelloOmitsFileManagementCapabilities(t *testing.T) {
	hello, err := BuildHello("agent", coretool.EmptyExecutor(), "agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range hello.Capabilities {
		if capability == "file.list" || capability == "file.mkdir" {
			t.Fatalf("regular agent advertised runner-only capability %q", capability)
		}
	}
}

func TestAgentRejectsUnsupportedFileOperation(t *testing.T) {
	envelope := aop.MustWrap("read-1", "", &filepb.ProtocolMessage{Message: &filepb.ProtocolMessage_ReadRequest{ReadRequest: &filepb.ReadRequest{Path: "proof.txt"}}})
	var response *aop.ProtocolMessage
	handleAgentFileMessage(connectionConfig{}, envelope, &filepb.ProtocolMessage{Message: &filepb.ProtocolMessage_ReadRequest{ReadRequest: &filepb.ReadRequest{Path: "proof.txt"}}}, func(replyTo string, message protobuf.Message) {
		if replyTo != envelope.Id {
			t.Fatalf("reply target = %q", replyTo)
		}
		response, _ = message.(*aop.ProtocolMessage)
	})
	if response.GetProtocolError().GetCode() != "OPERATION_FAILED" {
		t.Fatalf("response = %+v", response)
	}
}

func TestUploadWritesAbsolutePath(t *testing.T) {
	const filename = "cyber_test_upload_probe.txt"
	const body = "codex public proof\nkey=appImage/probe"
	dest := filepath.Join(os.TempDir(), "cyber-uploads", filename)
	t.Cleanup(func() { _ = os.Remove(dest) })
	result, err := uploadNodeFile(&filepb.UploadRequest{SessionId: "sess-1", Filename: filename, Data: []byte(body)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != dest {
		t.Fatalf("result = %+v, want path %q", result, dest)
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != body {
		t.Fatalf("file on disk = %q, err=%v; want %q", data, err, body)
	}
}

func TestShouldResetReconnectBackoff(t *testing.T) {
	connectedAt := time.Now()
	tests := []struct {
		name           string
		connectedAt    time.Time
		disconnectedAt time.Time
		want           bool
	}{
		{name: "dial failure", disconnectedAt: connectedAt},
		{name: "short session", connectedAt: connectedAt, disconnectedAt: connectedAt.Add(reconnectStableAfter - time.Second)},
		{name: "stable session", connectedAt: connectedAt, disconnectedAt: connectedAt.Add(reconnectStableAfter), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldResetReconnectBackoff(tt.connectedAt, tt.disconnectedAt); got != tt.want {
				t.Fatalf("shouldResetReconnectBackoff() = %t, want %t", got, tt.want)
			}
		})
	}
}

type writeFailureStream struct {
	helloID   string
	accepted  bool
	closed    chan struct{}
	closeOnce sync.Once
	sends     atomic.Int32
	err       error
}

func (s *writeFailureStream) Send(envelope *aop.Envelope) error {
	if s.sends.Add(1) == 1 {
		s.helloID = envelope.GetId()
		return nil
	}
	return s.err
}

func (s *writeFailureStream) Recv() (*aop.Envelope, error) {
	if !s.accepted {
		s.accepted = true
		return aop.MustWrap("accepted", s.helloID, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{NodeId: "runner-1"}}}), nil
	}
	<-s.closed
	return nil, io.ErrClosedPipe
}

func (s *writeFailureStream) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

func TestServeAgentConnectionClosesStreamAfterWriteFailure(t *testing.T) {
	wantErr := errors.New("write failed")
	stream := &writeFailureStream{closed: make(chan struct{}), err: wantErr}
	done := make(chan error, 1)
	go func() {
		done <- serveAgentConnection(context.Background(), connectionConfig{
			Name:   "runner-1",
			NodeID: "runner-1",
			Events: coreevents.New(),
			Menu:   func() []*types.CommandSpec { return nil },
		}, telemetry.NopLogger(), stream)
	}()

	select {
	case err := <-done:
		if !errors.Is(err, wantErr) {
			t.Fatalf("serveAgentConnection error = %v, want %v", err, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("write failure did not unblock the receive loop")
	}
}

type deadlineRecordingConn struct {
	net.Conn
	boundedReads  atomic.Int32
	boundedWrites atomic.Int32
}

func TestWebsocketLivenessUsesBoundedIntervals(t *testing.T) {
	if websocketPongWait <= 0 || websocketPingPeriod <= 0 || websocketWriteWait <= 0 {
		t.Fatal("websocket liveness intervals must be positive")
	}
	if websocketPingPeriod >= websocketPongWait {
		t.Fatalf("ping period %v must be shorter than pong wait %v", websocketPingPeriod, websocketPongWait)
	}
	if reconnectStableAfter <= websocketPongWait {
		t.Fatalf("stable window %v must exceed pong wait %v", reconnectStableAfter, websocketPongWait)
	}
}

func (c *deadlineRecordingConn) SetReadDeadline(deadline time.Time) error {
	if !deadline.IsZero() {
		c.boundedReads.Add(1)
	}
	return c.Conn.SetReadDeadline(deadline)
}

func (c *deadlineRecordingConn) SetWriteDeadline(deadline time.Time) error {
	if !deadline.IsZero() {
		c.boundedWrites.Add(1)
	}
	return c.Conn.SetWriteDeadline(deadline)
}

func TestWebSocketStreamSetsReadAndWriteDeadlines(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	recorded := make(chan *deadlineRecordingConn, 1)
	dialer := *websocket.DefaultDialer
	dialer.NetDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		wrapped := &deadlineRecordingConn{Conn: conn}
		recorded <- wrapped
		return wrapped, nil
	}
	dialURL, _, err := aopws.DialURL(server.URL, toolnode.DefaultWSPath)
	if err != nil {
		t.Fatal(err)
	}
	wsConn, response, err := dialer.DialContext(context.Background(), dialURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	stream, err := aopws.New(context.Background(), wsConn, aopws.Options{
		Encoding:     aopws.Binary,
		WriteTimeout: websocketWriteWait,
		PingInterval: websocketPingPeriod,
		PongTimeout:  websocketPongWait,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	conn := <-recorded
	if conn.boundedReads.Load() == 0 {
		t.Fatal("websocket dial did not install a bounded read deadline")
	}
	writesBefore := conn.boundedWrites.Load()
	if err := stream.Send(&aop.Envelope{Id: "deadline-probe"}); err != nil {
		t.Fatal(err)
	}
	if conn.boundedWrites.Load() <= writesBefore {
		t.Fatal("application write did not install a bounded write deadline")
	}
}

func TestWebSocketStreamClosesWhenContextEnds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(250 * time.Millisecond)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	stream, err := dialProtoWebSocket(ctx, connectionConfig{ServerURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	_, err = stream.Recv()
	if err == nil || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("Recv error = %v, context error = %v; want context deadline closure", err, ctx.Err())
	}
}

func TestConcreteRuntimeControlRepliesReachNodeConnection(t *testing.T) {
	app := apptest.NewFixture(t, telemetry.NopLogger(), nil)
	rt := sessionext.New(agentsession.Config{Logger: telemetry.NopLogger()})
	ns := namespaces.New()
	rtSet := hosttest.Set(t, append(apptest.Entries(t, app), ns, promptext.New(), loopext.New(agent.StandardLoop{}), rt, sessionext.NewProtocol())...)
	if err := rtSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer rtSet.Close(context.Background())
	stream := &namespaceReplyStream{
		sent: make(chan *aop.Envelope, 32),
		payload: aop.MustWrap("open-embedded", "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionRequest{
			OpenSessionRequest: &aop.OpenSessionRequest{SessionId: "embedded"},
		}}),
	}
	err := serveAgentConnection(context.Background(), connectionConfig{
		Name: "embedded", NodeID: "embedded", Events: app.Stream,
		RegisterNamespaces: ns.Bind,
	}, telemetry.NopLogger(), stream)
	if err != io.EOF {
		t.Fatalf("connection: %v", err)
	}
	for len(stream.sent) > 0 {
		envelope := <-stream.sent
		if envelope.ReplyTo != "open-embedded" {
			continue
		}
		message, err := aop.Unwrap(envelope)
		if err != nil {
			t.Fatal(err)
		}
		response, ok := message.(*aop.ProtocolMessage)
		if !ok || response.GetOpenSessionResponse().GetAccepted().GetId() != "embedded" {
			t.Fatalf("runtime control was not connected: %v", message)
		}
		return
	}
	t.Fatal("runtime control response never reached the connection")
}

type namespaceReplyStream struct {
	helloID string
	recvs   int
	sent    chan *aop.Envelope
	payload *aop.Envelope
}

func (s *namespaceReplyStream) Send(envelope *aop.Envelope) error {
	if s.helloID == "" {
		s.helloID = envelope.GetId()
	}
	select {
	case s.sent <- envelope:
	default:
	}
	return nil
}

func (s *namespaceReplyStream) Recv() (*aop.Envelope, error) {
	s.recvs++
	switch s.recvs {
	case 1:
		return aop.MustWrap("accepted", s.helloID, &aop.ProtocolMessage{
			Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{NodeId: "runner-1"}},
		}), nil
	case 2:
		return s.payload, nil
	}
	time.Sleep(200 * time.Millisecond)
	return nil, io.EOF
}

func TestTrafficNamespaceRepliesReachTheWire(t *testing.T) {
	stream := &namespaceReplyStream{
		sent: make(chan *aop.Envelope, 16),
		payload: aop.MustWrap("query-1", "", &trafficpb.ProtocolMessage{
			Message: &trafficpb.ProtocolMessage_Query{Query: &trafficpb.Query{State: true}},
		}),
	}
	ns := namespaces.New()
	registry := coretool.NewCommandRegistry()
	hosttest.Load(t, t.Context(), extension.Provided(hooks.New()), ns, registry, proxyext.New(proxyext.Config{WorkDir: t.TempDir()}))
	cc := connectionConfig{
		Name: "runner-1", NodeID: "runner-1", Events: coreevents.New(), RegisterNamespaces: ns.Bind,
	}

	if err := serveAgentConnection(context.Background(), cc, telemetry.NopLogger(), stream); err != io.EOF {
		t.Fatalf("serveAgentConnection error = %v, want EOF", err)
	}
	for {
		select {
		case envelope := <-stream.sent:
			message, err := aop.Unwrap(envelope)
			if err != nil {
				continue
			}
			value, ok := message.(*trafficpb.ProtocolMessage)
			if !ok {
				continue
			}
			if value.GetState().GetCapture().GetMode() == trafficpb.CaptureMode_CAPTURE_MODE_RELAY {
				if envelope.GetReplyTo() != "query-1" {
					t.Fatalf("reply_to = %q, want query-1", envelope.GetReplyTo())
				}
				return
			}
		default:
			t.Fatal("the namespace handler's reply never reached the wire")
		}
	}
}
