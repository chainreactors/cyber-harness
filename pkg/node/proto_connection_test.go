package node

import (
	"bytes"
	"context"
	"errors"
	"github.com/chainreactors/cyber/core/extension"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentsession "github.com/chainreactors/cyber/agent/session"
	aop "github.com/chainreactors/cyber/aop"
	execpb "github.com/chainreactors/cyber/aop/exec"
	filepb "github.com/chainreactors/cyber/aop/file"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	trafficpb "github.com/chainreactors/cyber/aop/traffic"
	"github.com/chainreactors/cyber/cmd/harness"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/eventbus"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	toolnode "github.com/chainreactors/cyber/pkg/node/tool"
	types "github.com/chainreactors/cyber/pkg/types"
	proxytool "github.com/chainreactors/cyber/tools/proxy"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
)

type singleDeliveryProbeTool struct{}

var testUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func testToolExecutor(t *testing.T, tools ...coretool.Tool) coretool.Executor {
	t.Helper()
	return harness.Tools(t, tools...)
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
		Registry: commands.NewRegistry(nil),
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
	operations := make(map[string]context.CancelFunc)
	var operationsMu sync.Mutex
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
	handleAgentToolMessage(
		context.Background(),
		connectionConfig{Registry: commands.NewRegistry(nil), Logger: logger, Agent: panicAgentEndpoint{}},
		&aop.Envelope{Id: "op-panic"},
		&toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Call{Call: request}},
		send, &operationsMu, operations, make(map[string]time.Time),
	)

	select {
	case got := <-failure:
		if got.Code != "OPERATION_FAILED" || !strings.Contains(got.Message, "unexpectedly") {
			t.Fatalf("failure = %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for operation failure")
	}
	operationsMu.Lock()
	_, tracked := operations["op-panic"]
	operationsMu.Unlock()
	if tracked {
		t.Fatal("panicking operation was not cleaned up")
	}
	if got := logs.String(); !strings.Contains(got, "send event boom") || !strings.Contains(got, "op-panic") {
		t.Fatalf("panic log = %s", got)
	}
}

// Canceling a call does not stop a scanner that ignores its context, so the
// hub having given up must also close that call's artifact window — otherwise
// the rest of the crawl crosses the wire only to be rejected on arrival.
func TestCancelOperationSealsTheCallArtifactWindow(t *testing.T) {
	var operationsMu sync.Mutex
	operations := make(map[string]context.CancelFunc)
	sealed := make(map[string]time.Time)
	canceled := false
	operations["op-1"] = func() { canceled = true }

	intercepted, err := interceptCancelOperation(
		aop.MustWrap("cancel-1", "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CancelOperation{CancelOperation: &aop.CancelOperation{TargetId: "op-1"}}}),
		&operationsMu, operations, sealed,
	)
	if err != nil || !intercepted {
		t.Fatalf("intercept cancel: intercepted=%v err=%v", intercepted, err)
	}

	if !canceled {
		t.Fatal("cancel did not reach the operation")
	}
	if !callIsSealed(&operationsMu, sealed, "op-1") {
		t.Fatal("canceled call was left able to emit artifacts")
	}
	if callIsSealed(&operationsMu, sealed, "agent-loop-call") {
		t.Fatal("a call this connection never dispatched must not be sealed")
	}
}

func TestManagerToolResultUsesSingleDeliveryPath(t *testing.T) {
	ctx := context.Background()
	app := newTestApp(t, telemetry.NopLogger(), apppkg.Dependencies{})

	appSet := loadNodeTestApplication(t, ctx, app)
	defer appSet.Close(context.Background())
	rt, err := sessionext.New(agentsession.Config{Application: app, Option: &cfg.Option{}, Logger: telemetry.NopLogger()})
	if err != nil {
		t.Fatal(err)
	}

	rtSet := harness.Set(t, rt)
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
	handleAgentToolMessage(
		ctx,
		connectionConfig{
			Executor: registry,
			Logger:   telemetry.NopLogger(),
			Agent:    rt.Runtime(),
		},
		&aop.Envelope{Id: "single-delivery-op"},
		&toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Call{Call: request}},
		send,
		&sync.Mutex{},
		make(map[string]context.CancelFunc),
		make(map[string]time.Time),
	)

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

func TestExecRequestCompletesWithOutput(t *testing.T) {
	command := "printf hello"
	if runtime.GOOS == "windows" {
		command = "echo|set /p=hello"
	}
	var messages []*execpb.ProtocolMessage
	handleExecRequest(context.Background(), &execpb.Request{Command: command, TimeoutSeconds: 5}, t.TempDir(), "exec-1", func(_ string, message protobuf.Message) {
		if value, ok := message.(*execpb.ProtocolMessage); ok {
			messages = append(messages, value)
		}
	})
	if len(messages) != 2 || string(messages[0].GetOutput().Data) != "hello" || messages[1].GetResult().State != "completed" {
		t.Fatalf("unexpected messages: %#v", messages)
	}
}

func TestExecRequestReportsExitCode(t *testing.T) {
	command := "exit 7"
	if runtime.GOOS == "windows" {
		command = "exit /b 7"
	}
	var result *execpb.Result
	handleExecRequest(context.Background(), &execpb.Request{Command: command, TimeoutSeconds: 5}, t.TempDir(), "exec-2", func(_ string, message protobuf.Message) {
		if value, ok := message.(*execpb.ProtocolMessage); ok && value.GetResult() != nil {
			result = value.GetResult()
		}
	})
	if result == nil || result.ExitCode != 7 {
		t.Fatalf("result = %+v, want exit code 7", result)
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
			Registry: commands.NewRegistry(nil),
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
	stream, err := newWebSocketEnvelopeStream(wsConn, false)
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

func TestWebSocketStreamTimesOutSilentPeer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(250 * time.Millisecond)
	}))
	defer server.Close()

	stream, err := dialProtoWebSocket(context.Background(), connectionConfig{ServerURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err = stream.Recv()
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("Recv error = %v, want timeout", err)
	}
}

func loadNodeTestApplication(t *testing.T, ctx context.Context, application *apppkg.App) *extension.Set {
	return harness.AppLoad(t, ctx, application)
}

func TestConcreteRuntimeControlRepliesReachNodeConnection(t *testing.T) {
	app := newTestApp(t, telemetry.NopLogger(), apppkg.Dependencies{})
	appSet := loadNodeTestApplication(t, t.Context(), app)
	defer appSet.Close(context.Background())
	rt, err := sessionext.New(agentsession.Config{Application: app, Option: &cfg.Option{}, Logger: telemetry.NopLogger()})
	if err != nil {
		t.Fatal(err)
	}
	rtSet := harness.Set(t, rt)
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
	err = serveAgentConnection(context.Background(), connectionConfig{
		Name: "embedded", NodeID: "embedded", Registry: app.Commands, Agent: rt.Runtime(),
		RegisterNamespaces: func(mux *aop.NamespaceMux) error {
			for _, binding := range rt.Runtime().NamespaceBindings() {
				if err := binding.Register(mux); err != nil {
					return err
				}
			}
			return nil
		},
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
	hub := proxytool.NewProxyHub(proxytool.NewState(""), proxytool.NewFlowStore(8), t.TempDir(), false, nil)
	defer hub.Close(context.Background())
	cc := connectionConfig{
		Name: "runner-1", NodeID: "runner-1",
		Registry: commands.NewRegistry(nil), Agent: newSilentAgentEndpoint(),
		RegisterNamespaces: func(mux *aop.NamespaceMux) error {
			binding, err := proxytool.TrafficNamespace(hub.ProxyHub)
			if err != nil {
				return err
			}
			return binding.Register(mux)
		},
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
