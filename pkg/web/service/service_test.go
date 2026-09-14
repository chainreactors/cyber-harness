package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"connectrpc.com/connect"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/extension"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	sessionext "github.com/chainreactors/aiscan/pkg/exts/session"
	profile "github.com/chainreactors/aiscan/pkg/profile"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	types "github.com/chainreactors/aiscan/pkg/types"
)

func TestScanArgsForSelectedAnalysisOptions(t *testing.T) {
	scan := &types.Scan{
		Target:  "127.0.0.1",
		Mode:    "full",
		Options: &types.ScanOptions{Verify: true, Sniper: true, Deep: true},
	}

	got := scanArgsForScan(scan)
	want := []string{"-i", "127.0.0.1", "--mode", "full", "--verify=high", "--sniper", "--deep"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scan args = %#v, want %#v", got, want)
	}
}

func TestServiceStatusReportsLLMAvailability(t *testing.T) {
	service := NewService(ServiceConfig{})
	if service.Status().GetLlmAvailable() {
		t.Fatal("LLMAvailable = true, want false without provider")
	}
}

func TestRunTurnRejectsMissingSessionBeforePersisting(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := NewService(ServiceConfig{Store: store})

	response, err := svc.api.Sessions.RunTurn(context.Background(), "run-1", &aop.RunTurnRequest{
		SessionId: "missing", TurnId: "turn-1",
		Input: &aop.Message{Role: "user", Content: []*aop.Content{{Value: &aop.Content_Text{Text: &aop.TextContent{Text: "hello"}}}}},
	})
	if err != nil || response.GetRejected().GetCode() != "NOT_FOUND" {
		t.Fatalf("RunTurn = %v, %v", response, err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM chat_aop_events WHERE session_id = 'missing'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("missing session retained %d events", count)
	}
}

func TestRemovedChatAndScanRoutesReturnNotFoundBeforeSPAFallback(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := NewService(ServiceConfig{Store: store})
	handler := newHandler(svc, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), "")
	for _, test := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/chat"},
		{method: http.MethodGet, path: "/api/chat/sessions"},
		{method: http.MethodPost, path: "/api/chat/sessions/missing/messages"},
		{method: http.MethodGet, path: "/api/scans"},
		{method: http.MethodGet, path: "/api/scans/missing/events"},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, body = %s; want 404", test.method, test.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestParseCommand(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantCmd string
		wantArg string
		wantOK  bool
	}{
		{"scan with target", "/scan example.com", "scan", "example.com", true},
		{"scan with flags", "/scan example.com --mode full --deep", "scan", "example.com --mode full --deep", true},
		{"verb only", "/agents", "agents", "", true},
		{"lowercased verb", "/SCAN Example.com", "scan", "Example.com", true},
		{"extra spaces", "/scan    a.com   b.com", "scan", "a.com   b.com", true},
		{"tab separator", "/help\tx", "help", "x", true},
		{"plain message", "hello there", "", "", false},
		{"bare slash", "/", "", "", false},
		{"slash then spaces", "/   ", "", "", false},
		{"path-like, not a command", "/etc/passwd", "etc/passwd", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, arg, ok := parseCommand(tc.in)
			if ok != tc.wantOK || cmd != tc.wantCmd || arg != tc.wantArg {
				t.Fatalf("parseCommand(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.in, cmd, arg, ok, tc.wantCmd, tc.wantArg, tc.wantOK)
			}
		})
	}
}

func newMenuTestService(t *testing.T) *Service {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewService(ServiceConfig{Store: store})
}

// TestSessionMenuMergeAndFallback checks the "/" menu the hub serves: hub-scope
// commands merged with the agent's (here, the static fallback since no agent is
// bound), with run-control commands excluded.
func TestSessionMenuMergeAndFallback(t *testing.T) {
	svc := newMenuTestService(t)
	names := map[string]bool{}
	for _, s := range svc.SessionMenu("no-such-session") {
		names[s.Name] = true
	}
	for _, want := range []string{"/agents", "/help", "/status", "/provider", "/model"} {
		if !names[want] {
			t.Errorf("SessionMenu missing %q", want)
		}
	}
	for _, absent := range []string{"/scan", "/stop", "/continue", "/eval", "/followup", "/loop"} {
		if names[absent] {
			t.Errorf("SessionMenu leaked run-control command %q", absent)
		}
	}
}

// TestSessionCommandsConnectRPC drives the generated Connect endpoint the
// frontend "/" menu uses and proves it returns the protobuf command catalog.
func TestSessionCommandsConnectRPC(t *testing.T) {
	svc := newMenuTestService(t)
	srv := httptest.NewServer(newHandler(svc, nil, nil, ""))
	defer srv.Close()

	client := rpc.NewSessionServiceClient(srv.Client(), srv.URL, connect.WithProtoJSON())
	resp, err := client.ListCommands(context.Background(), connect.NewRequest(&types.ListCommandsRequest{SessionId: "anything"}))
	if err != nil {
		t.Fatalf("ListCommands: %v", err)
	}
	names := map[string]bool{}
	for _, s := range resp.Msg.Commands {
		names[s.Name] = true
	}
	for _, want := range []string{"/help", "/status", "/model"} {
		if !names[want] {
			t.Errorf("ListCommands response missing %q (got %d specs)", want, len(resp.Msg.Commands))
		}
	}
	if names["/scan"] {
		t.Error("ListCommands leaked deferred scan command")
	}
}

type sessionProbe struct {
	sid       string
	found     bool
	aopEvents []*aop.Event
}

func (s *sessionProbe) TaskSession(string) (string, bool) { return s.sid, s.found }
func (s *sessionProbe) BroadcastAOPEvent(_ string, event *aop.Event) {
	s.aopEvents = append(s.aopEvents, event)
}

func TestForwardAgentEventKeepsEvalOnlyInAOP(t *testing.T) {
	probe := &sessionProbe{sid: "sess-eval", found: true}
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(probe)
	remote := &remoteAgent{nodeState: newNodeState(), nodeID: "agent-1", name: "worker"}

	event := &aop.Event{
		SessionId: "agent-session", TurnId: "turn-1", Emitter: "test-agent",
		Payload: &aop.Event_Status{Status: &aop.Status{State: types.EvalStateEnd}},
	}
	_ = types.SetEvalDetail(event, &types.EvalDetail{Round: 1, Pass: true, Reason: "found SQLi"})
	_ = types.SetCompactDetail(event, &types.CompactDetail{TokensBefore: 1000, TokensAfter: 400, KeptMessages: 8})
	pool.forwardAOPFrame(remote, "turn-1", event)

	if len(probe.aopEvents) == 0 {
		t.Fatal("AOP event was not forwarded")
	}
	evalDetail, ok, err := types.GetEvalDetail(probe.aopEvents[0])
	if err != nil || !ok {
		t.Fatalf("eval extension = %#v, %v, %v", probe.aopEvents[0].Extensions, ok, err)
	}
	if evalDetail.Round != 1 || !evalDetail.Pass || evalDetail.Reason != "found SQLi" {
		t.Fatalf("eval detail = %#v", evalDetail)
	}
	compactDetail, ok, err := types.GetCompactDetail(probe.aopEvents[0])
	if err != nil || !ok || compactDetail.TokensBefore != 1000 || compactDetail.KeptMessages != 8 {
		t.Fatalf("compact detail = %#v, %v, %v", compactDetail, ok, err)
	}
}

func TestForwardStandaloneScanAOPDoesNotCreateChatHistory(t *testing.T) {
	probe := &sessionProbe{}
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(probe)
	event := &aop.Event{SessionId: "scan-not-chat", Emitter: "worker", Payload: &aop.Event_Status{Status: &aop.Status{State: "running"}}}
	pool.forwardAOPFrame(&remoteAgent{nodeState: newNodeState()}, "scan-not-chat", event)
	if len(probe.aopEvents) != 0 {
		t.Fatalf("standalone scan AOP was forwarded to chat history: %+v", probe.aopEvents)
	}
}

func TestForwardUncorrelatedEventForAgentOpenSession(t *testing.T) {
	probe := &sessionProbe{}
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(probe)
	state := newNodeState()
	state.openSessions["session-command"] = struct{}{}
	remote := &remoteAgent{nodeState: state}
	event := &aop.Event{
		SessionId: "session-command",
		Emitter:   "worker",
		Payload: &aop.Event_Message{Message: &aop.Message{
			Id: "command-result", Role: "assistant", Content: []*aop.Content{aop.Text("Session: session-command")},
		}},
	}

	pool.forwardAOPFrame(remote, "", event)

	if len(probe.aopEvents) != 1 || probe.aopEvents[0].GetMessage().GetId() != "command-result" {
		t.Fatalf("uncorrelated command event was not forwarded: %+v", probe.aopEvents)
	}
}

type recordingProfile struct {
	assembly *profile.Assembly
	app      *apppkg.App
}

func (p *recordingProfile) Load(ctx context.Context) error  { return p.assembly.Load(ctx) }
func (p *recordingProfile) Close(ctx context.Context) error { return p.assembly.Close(ctx) }
func (p *recordingProfile) App() (*apppkg.App, error) {
	if !p.assembly.Available() {
		return nil, errors.New("profile is unavailable")
	}
	return p.app, nil
}
func (*recordingProfile) Runtime() (*sessionext.Manager, error) {
	return nil, errors.New("test profile has no runtime")
}
func (*recordingProfile) RegisterResourceNamespaces(*aop.NamespaceMux) error { return nil }

func newRecordingProfile(t *testing.T) (profile.Application, *apppkg.App, func() bool) {
	t.Helper()
	resource := apppkg.New(apppkg.Config{SkipEngines: true}, apppkg.Dependencies{})
	assembly, err := profile.Assemble(extension.Entry{ID: "application", Extension: resource})
	if err != nil {
		t.Fatal(err)
	}
	p := &recordingProfile{assembly: assembly, app: resource.App}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	if err := p.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	app, err := p.App()
	if err != nil {
		t.Fatal(err)
	}
	return p, app, func() bool {
		return app.Closed()
	}
}

func TestSwapAppDefersOldCloseUntilActiveLeaseReleases(t *testing.T) {
	old, oldApp, oldClosed := newRecordingProfile(t)
	next, _, _ := newRecordingProfile(t)
	svc := NewService(ServiceConfig{Profile: old})
	defer svc.Close(context.Background())

	leased, release := svc.acquireApp()
	if leased != oldApp {
		t.Fatal("acquireApp() returned the wrong app")
	}
	if err := svc.swapProfile(next); err != nil {
		t.Fatal(err)
	}
	if oldClosed() {
		t.Fatal("old app closed while a scan still held a lease")
	}
	release()
	if !oldClosed() {
		t.Fatal("old app remained open after the final lease released")
	}
}

func TestServiceCloseRetainsLeasedProfileAndRetries(t *testing.T) {
	p, app, closed := newRecordingProfile(t)
	svc := NewService(ServiceConfig{Profile: p})
	leased, release := svc.acquireApp()
	defer release()
	if leased != app {
		t.Fatal("wrong shared app")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.Close(ctx); !errors.Is(err, extension.ErrCloseIncomplete) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Close = %v", err)
	}
	if closed() {
		t.Fatal("profile closed while leased")
	}
	if next, done := svc.acquireApp(); next != nil {
		done()
		t.Fatal("service admitted work after closing")
	}
	release()
	release() // A request can only release its lease once.
	if err := svc.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !closed() {
		t.Fatal("profile remained open after release")
	}
	if len(svc.profiles) != 0 {
		t.Fatal("completed profiles remained owned")
	}
}

func TestSwapProfileRejectsClosingServiceWithoutTakingOwnership(t *testing.T) {
	svc := NewService(ServiceConfig{})
	if err := svc.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	candidate, _, closed := newRecordingProfile(t)
	if err := svc.swapProfile(candidate); err == nil {
		t.Fatal("closing service accepted a profile")
	}
	if _, err := candidate.App(); err != nil {
		t.Fatalf("rejected candidate was closed by service: %v", err)
	}
	if closed() {
		t.Fatal("service released a candidate it did not own")
	}
	if len(svc.profiles) != 0 {
		t.Fatal("service retained rejected candidate")
	}
}
