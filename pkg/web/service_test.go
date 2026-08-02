package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	scanpb "github.com/chainreactors/aiscan/pkg/types/scan"
)

func TestScanArgsForSelectedAnalysisOptions(t *testing.T) {
	scan := &scanpb.Scan{
		Target:  "127.0.0.1",
		Mode:    "full",
		Options: &scanpb.ScanOptions{Verify: true, Sniper: true, Deep: true},
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

	response, err := NewAOPChatServer(svc).RunTurn(context.Background(), "run-1", &aop.RunTurnRequest{
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
	handler := NewHandler(svc, nil, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
