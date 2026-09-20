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
	"github.com/chainreactors/cyber/core/eventbus"
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

type trackingAgentEndpoint struct {
	bus        *eventbus.Bus[*aop.Event]
	subscribed *bool
}

func (e *trackingAgentEndpoint) Observe(observer coreevents.Observer) *eventbus.Subscription[*aop.Event] {
	*e.subscribed = true
	return e.bus.Subscribe(observer.ObserveEvent)
}

func (e *trackingAgentEndpoint) Publish(event *aop.Event) { e.bus.Emit(event) }

type silentAgentEndpoint struct{ bus *eventbus.Bus[*aop.Event] }

func newSilentAgentEndpoint() *silentAgentEndpoint {
	return &silentAgentEndpoint{bus: eventbus.New[*aop.Event]()}
}

func (e *silentAgentEndpoint) Observe(observer coreevents.Observer) *eventbus.Subscription[*aop.Event] {
	return e.bus.Subscribe(observer.ObserveEvent)
}

func (e *silentAgentEndpoint) Publish(event *aop.Event) { e.bus.Emit(event) }

type panicAgentEndpoint struct{}

func (panicAgentEndpoint) Observe(coreevents.Observer) *eventbus.Subscription[*aop.Event] {
	return nil
}
func (panicAgentEndpoint) Publish(*aop.Event) { panic("send event boom") }

type handshakeThenEOFStream struct {
	helloID string
	recvs   int
}

func (s *handshakeThenEOFStream) Send(envelope *aop.Envelope) error {
	if s.helloID == "" {
		s.helloID = envelope.GetId()
	}
	return nil
}

func (s *handshakeThenEOFStream) Recv() (*aop.Envelope, error) {
	s.recvs++
	if s.recvs == 1 {
		return aop.MustWrap("accepted", s.helloID, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{NodeId: "runner-1"}}}), nil
	}
	return nil, io.EOF
}

func TestServeAgentConnectionSubscribesBeforePublishingMenu(t *testing.T) {
	stream := new(handshakeThenEOFStream)
	subscribed := false
	menuCalled := false
	cc := connectionConfig{
		Name:     "runner-1",
		NodeID:   "runner-1",
		Registry: coretool.NewCommandRegistry(),
		Agent:    &trackingAgentEndpoint{bus: eventbus.New[*aop.Event](), subscribed: &subscribed},
		Menu: func() []*types.CommandSpec {
			menuCalled = true
			if !subscribed {
				t.Error("command catalog was published before event subscription")
			}
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
	handler := &toolnode.CallHandler{Executor: coretool.EmptyExecutor(), Logger: logger, Publish: panicAgentEndpoint{}.Publish, Send: send}
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
	unsubscribe := rt.Runtime().Observe(coreevents.ObserverFunc(func(event *aop.Event) {
		if event == nil {
			return
		}
		if event.GetToolCall() != nil {
			runtimeToolCalls.Add(1)
		}
		if event.GetToolResult() != nil {
			runtimeEvents <- event
		}
	}))
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

func TestDefaultManagerDoesNotAdvertiseRunnerFileRPCs(t *testing.T) {
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

func TestFileListReturnsStructuredEntries(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "note.txt"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	value := fileList(&filepb.ListRequest{Path: "."}, base)
	if value.err != nil {
		t.Fatal(value.err)
	}
	if value.result.Path != "." || len(value.result.Entries) != 2 {
		t.Fatalf("result = %+v", value.result)
	}
	byName := map[string]*filepb.Entry{}
	for _, entry := range value.result.Entries {
		byName[entry.Name] = entry
	}
	if byName["note.txt"].IsDirectory || byName["note.txt"].Size != 4 {
		t.Fatalf("file entry = %+v", byName["note.txt"])
	}
	if !byName["nested"].IsDirectory {
		t.Fatalf("directory entry = %+v", byName["nested"])
	}
}

func TestNativeFileRPCsResolveRelativeToRuntimeWorkdir(t *testing.T) {
	base := t.TempDir()
	if value := fileMkdir(&filepb.MkdirRequest{Path: "nested"}, base); value.err != nil {
		t.Fatal(value.err)
	}
	path := filepath.Join("nested", "proof.txt")
	if value := fileWrite(&filepb.WriteRequest{Path: path, Data: []byte("hello")}, base); value.err != nil {
		t.Fatal(value.err)
	}
	value := fileRead(&filepb.ReadRequest{Path: path}, base)
	if value.err != nil || string(value.result.Data) != "hello" {
		t.Fatalf("read data = %q, err = %v", value.result.Data, value.err)
	}
}

func TestFileReadReturnsBoundedChunks(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "capture.mp4")
	data := bytes.Repeat([]byte("frame"), 300_000)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	first := fileRead(&filepb.ReadRequest{Path: path, Limit: 256 * 1024}, base)
	if first.err != nil {
		t.Fatal(first.err)
	}
	if first.result.Offset != 0 || first.result.Eof || len(first.result.Data) != 256*1024 || first.result.Size != int64(len(data)) {
		t.Fatalf("first chunk = %+v, bytes=%d", first.result, len(first.result.Data))
	}
	if first.result.MediaType != "video/mp4" {
		t.Fatalf("media type = %q, want video/mp4", first.result.MediaType)
	}
	joined := append([]byte(nil), first.result.Data...)
	offset := int64(len(joined))
	for {
		next := fileRead(&filepb.ReadRequest{Path: path, Offset: offset, Limit: maxFileReadChunkBytes + 1}, base)
		if next.err != nil {
			t.Fatal(next.err)
		}
		if next.result.Offset != offset || len(next.result.Data) > int(maxFileReadChunkBytes) {
			t.Fatalf("chunk offset=%d bytes=%d, want offset=%d max=%d", next.result.Offset, len(next.result.Data), offset, maxFileReadChunkBytes)
		}
		joined = append(joined, next.result.Data...)
		offset += int64(len(next.result.Data))
		if next.result.Eof {
			break
		}
	}
	if !bytes.Equal(joined, data) {
		t.Fatalf("joined bytes = %d, want %d", len(joined), len(data))
	}
}

func TestFileReadDoesNotDecodePathEncodedRanges(t *testing.T) {
	encoded := "aop-range://read?path=proof.txt&offset=1&limit=2"
	value := fileRead(&filepb.ReadRequest{Path: encoded}, t.TempDir())
	if value.err == nil {
		t.Fatal("path-encoded range unexpectedly succeeded")
	}
	if value.result.Path != encoded {
		t.Fatalf("result path = %q, want original path %q", value.result.Path, encoded)
	}
}

func TestUploadWritesAbsolutePath(t *testing.T) {
	const filename = "cyber_test_upload_probe.txt"
	const body = "codex public proof\nkey=appImage/probe"
	dest := filepath.Join(os.TempDir(), "cyber-uploads", filename)
	t.Cleanup(func() { _ = os.Remove(dest) })
	result, err := (&chatAgentHandler{}).Upload(&filepb.UploadRequest{SessionId: "sess-1", Filename: filename, Data: []byte(body)})
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
			Name:     "runner-1",
			NodeID:   "runner-1",
			Registry: coretool.NewCommandRegistry(),
			Agent:    newSilentAgentEndpoint(),
			Menu:     func() []*types.CommandSpec { return nil },
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
	wsConn, response, err := dialer.DialContext(context.Background(), HTTPToWS(server.URL)+toolnode.DefaultWSPath, nil)
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
		Name: "embedded", NodeID: "embedded", Registry: rt.Runtime().CommandRegistry(), Agent: rt.Runtime(),
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
		Name: "runner-1", NodeID: "runner-1", Registry: registry, Agent: newSilentAgentEndpoint(), RegisterNamespaces: ns.Bind,
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
