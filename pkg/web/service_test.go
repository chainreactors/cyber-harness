package web

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	aop "github.com/chainreactors/aiscan/aop"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	types "github.com/chainreactors/aiscan/pkg/types"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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

func TestLegacyChatAndScanRoutesReturnNotFoundBeforeSPAFallback(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := NewService(ServiceConfig{Store: store})
	handler := NewHandler(svc, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

func TestBuildMarkdownReportUsesLibcstxFacts(t *testing.T) {
	nodes := []json.RawMessage{
		json.RawMessage(`{"cstx_type":"ip","cstx_id":"ip:111.63.65.103","ip":"111.63.65.103"}`),
		json.RawMessage(`{"cstx_type":"port","cstx_id":"port:111.63.65.103:80:tcp","ip":"111.63.65.103","port":"80","protocol":"tcp"}`),
		json.RawMessage(`{"cstx_type":"url","cstx_id":"url:http://111.63.65.103/","scheme":"http","host":"111.63.65.103","path":"/","status_code":200,"title":"BWS/1.1"}`),
		json.RawMessage(`{"cstx_type":"framework","cstx_id":"framework:bws","name":"BWS","version":"1.1"}`),
		json.RawMessage(`{"cstx_type":"vuln","cstx_id":"vuln:test","value":"CVE-TEST","name":"Example finding","severity":"high","url":"http://111.63.65.103/"}`),
	}

	zh := buildMarkdownReport("baidu.com", "quick", nodes, "zh")
	for _, want := range []string{"# 扫描报告", "## 概览", "快速", "## 端口", "## WEB", "## 框架", "## 漏洞"} {
		if !strings.Contains(zh, want) {
			t.Fatalf("zh report missing %q:\n%s", want, zh)
		}
	}
	for _, old := range []string{"Asset", "Service", "WebProbe", "Loot"} {
		if strings.Contains(zh, old) {
			t.Fatalf("report leaked removed AIScan taxonomy %q:\n%s", old, zh)
		}
	}

	en := buildMarkdownReport("baidu.com", "quick", nodes, "en")
	for _, want := range []string{"# Scan Report", "## Overview", "Quick", "## Ports", "## Web", "## Frameworks", "## Vulnerabilities"} {
		if !strings.Contains(en, want) {
			t.Fatalf("en report missing %q:\n%s", want, en)
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
	srv := httptest.NewServer(NewHandler(svc, nil, nil, ""))
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

type evalSink struct {
	sid       string
	found     bool
	aopEvents []*aop.Event
}

func (s *evalSink) TaskSession(string) (string, bool) { return s.sid, s.found }
func (s *evalSink) BroadcastAOPEvent(_ string, event *aop.Event) {
	s.aopEvents = append(s.aopEvents, event)
}

func TestForwardAgentEventKeepsEvalOnlyInAOP(t *testing.T) {
	sink := &evalSink{sid: "sess-eval", found: true}
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(sink)
	remote := &remoteAgent{nodeState: newNodeState(), nodeID: "agent-1", name: "worker"}

	event := &aop.Event{
		SessionId: "agent-session", TurnId: "turn-1", Emitter: "test-agent",
		Payload: &aop.Event_Status{Status: &aop.Status{State: types.EvalStateEnd}},
	}
	_ = types.SetEvalDetail(event, types.EvalDetail{Round: 1, Pass: true, Reason: "found SQLi"})
	_ = types.SetCompactDetail(event, types.CompactDetail{TokensBefore: 1000, TokensAfter: 400, KeptMessages: 8})
	pool.forwardAOPFrame(remote, "turn-1", event)

	if len(sink.aopEvents) == 0 {
		t.Fatal("AOP event was not forwarded")
	}
	evalDetail, ok, err := types.GetEvalDetail(sink.aopEvents[0])
	if err != nil || !ok {
		t.Fatalf("eval extension = %#v, %v, %v", sink.aopEvents[0].Extensions, ok, err)
	}
	if evalDetail.Round != 1 || !evalDetail.Pass || evalDetail.Reason != "found SQLi" {
		t.Fatalf("eval detail = %#v", evalDetail)
	}
	compactDetail, ok, err := types.GetCompactDetail(sink.aopEvents[0])
	if err != nil || !ok || compactDetail.TokensBefore != 1000 || compactDetail.KeptMessages != 8 {
		t.Fatalf("compact detail = %#v, %v, %v", compactDetail, ok, err)
	}
}

func TestForwardStandaloneScanAOPDoesNotCreateChatHistory(t *testing.T) {
	sink := &evalSink{}
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(sink)
	event := &aop.Event{SessionId: "scan-not-chat", Emitter: "worker", Payload: &aop.Event_Status{Status: &aop.Status{State: "running"}}}
	pool.forwardAOPFrame(&remoteAgent{nodeState: newNodeState()}, "scan-not-chat", event)
	if len(sink.aopEvents) != 0 {
		t.Fatalf("standalone scan AOP was forwarded to chat history: %+v", sink.aopEvents)
	}
}

func TestForwardUncorrelatedEventForAgentOpenSession(t *testing.T) {
	sink := &evalSink{}
	pool := NewAgentPool(NewHub())
	pool.SetSessionLookup(sink)
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

	if len(sink.aopEvents) != 1 || sink.aopEvents[0].GetMessage().GetId() != "command-result" {
		t.Fatalf("uncorrelated command event was not forwarded: %+v", sink.aopEvents)
	}
}
