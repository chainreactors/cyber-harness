package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/audit/internal/toolchain"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func testOption(t *testing.T, url string) cfg.Option {
	t.Helper()
	_, option, err := parseOptions([]string{"--provider", "openai", "--base-url", url, "--api-key", "fixture", "--model", "fixture", "--data-dir", t.TempDir()}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	option.Context = &cfg.Context{Directory: t.TempDir(), Home: t.TempDir(), LookupEnv: func(string) (string, bool) { return "", false }}
	if _, err := cfg.ResolveRuntimeConfig(&option); err != nil {
		t.Fatal(err)
	}
	return option
}
func fakeTools(context.Context, string, io.Writer) ([]toolchain.Status, error) {
	return []toolchain.Status{{Name: "rg", Version: "15.2.0"}, {Name: "ast-grep", Version: "0.45.3"}, {Name: "osv-scanner", Version: "2.6.0"}}, nil
}
func toolReply(w http.ResponseWriter, name string, args any) {
	body, _ := json.Marshal(args)
	chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("call-%d", time.Now().UnixNano()), "type": "function", "function": map[string]any{"name": name, "arguments": string(body)}}}}, "finish_reason": "tool_calls"}}})
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
}
func streamReply(w http.ResponseWriter, text string) {
	chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}}})
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
}
func textReply(w http.ResponseWriter, text string) {
	json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}}})
}

func TestOneShotAuditToolReportAndResume(t *testing.T) {
	workspace := t.TempDir()
	dataDir := t.TempDir()
	reportDir := filepath.Join(workspace, "report")
	if err := os.WriteFile(filepath.Join(workspace, "handler.ts"), []byte("export function handler(input: string) { return input; }"), 0600); err != nil {
		t.Fatal(err)
	}
	c := coverage{Reviewed: true, Scope: "handler.ts fixture", Examined: []string{"handler.ts"}, Excluded: []string{}, Unsupported: []string{}, Unresolved: []string{}, Checks: []checkRecord{{Tool: "osv-scanner", Status: "not_applicable", Evidence: "No manifest in fixture"}, {Tool: "proton", Status: "completed", Evidence: "raw/proton.jsonl"}}}
	coverageJSON, _ := json.Marshal(c)
	steps := []struct {
		name string
		args any
	}{
		{"read", map[string]any{"path": "handler.ts"}},
		{"bash", map[string]any{"command": "proton -i handler.ts -c keys -j --no-stats -o report/raw/proton.jsonl"}},
		{"write", map[string]any{"path": "report/coverage.json", "content": string(coverageJSON)}},
		{"write", map[string]any{"path": "report/index.md", "content": "---\nokf_version: \"0.2\"\n---\n\n# Fixture audit\n\nNo confirmed vulnerability in the reviewed function. See [coverage](coverage.json), [findings](findings.json) and [log](log.md).\n"}},
		{"bash", map[string]any{"command": "okf validate report"}},
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), `"ping"`) && !strings.Contains(string(body), "cyber-audit") {
			textReply(w, "pong")
			return
		}
		step := int(requests.Add(1)) - 1
		if step == 0 {
			for _, want := range []string{"cyber-audit", "# Audit workflow", "ast-grep", "proton", "candidate"} {
				if !strings.Contains(string(body), want) {
					t.Errorf("audit prompt missing %q", want)
				}
			}
			if strings.Contains(string(body), "scan -i 192.168") {
				t.Error("scanner prompt leaked into audit")
			}
		}
		if step < len(steps) {
			toolReply(w, steps[step].name, steps[step].args)
		} else {
			streamReply(w, "Fixture audit completed; report saved.")
		}
	}))
	defer server.Close()
	var out, stderr strings.Builder
	err := run(t.Context(), []string{"--provider", "openai", "--base-url", server.URL, "--api-key", "fixture", "--model", "fixture", "--workdir", workspace, "--data-dir", dataDir, "--report-dir", reportDir, "-p", "Audit handler.ts", "--quiet", "--no-color", "--timeout", "30"}, &out, &stderr, fakeTools)
	if err != nil {
		t.Fatalf("run: %v; %s", err, stderr.String())
	}
	if requests.Load() != int32(len(steps)+1) {
		t.Fatalf("requests: %d", requests.Load())
	}
	historyPath := filepath.Join(reportDir, "session.jsonl")
	history, err := agentsession.ReadHistory(historyPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Messages) < 5 {
		t.Fatalf("missing tool history: %d", len(history.Messages))
	}
	option := testOption(t, server.URL)
	option.Resume = historyPath
	next, err := newReport(t.Context(), workspace, "continued", "", historyPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := newAuditProfile(option, telemetry.NopLogger(), workspace, 10, next)
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer profile.Close(context.Background())
	session, err := profile.runtime.OpenSession(t.Context(), agentsession.SessionOptions{ID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(session.MessagesSnapshot()) < len(history.Messages) {
		t.Fatal("resume discarded history")
	}
}
func TestRequiredToolFailurePrecedesModel(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); textReply(w, "unexpected") }))
	defer server.Close()
	fail := func(context.Context, string, io.Writer) ([]toolchain.Status, error) {
		return nil, errors.New("required tool missing")
	}
	err := run(t.Context(), []string{"--provider", "openai", "--base-url", server.URL, "--api-key", "fixture", "--model", "fixture", "--workdir", t.TempDir(), "--data-dir", t.TempDir(), "-p", "audit"}, io.Discard, io.Discard, fail)
	if err == nil || requests.Load() != 0 {
		t.Fatalf("err=%v requests=%d", err, requests.Load())
	}
}
func TestInteractiveProfileCommandsAndCancellation(t *testing.T) {
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"ping"`) && !strings.Contains(string(body), "cyber-audit") {
			textReply(w, "pong")
			return
		}
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer server.Close()
	workspace := t.TempDir()
	option := testOption(t, server.URL)
	report, err := newReport(t.Context(), workspace, "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := newAuditProfile(option, telemetry.NopLogger(), workspace, 10, report)
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer profile.Close(context.Background())
	for _, name := range []string{"proton", "arsenal", "okf"} {
		if !profile.runtime.CommandRegistry().Has(name) {
			t.Fatalf("missing %s", name)
		}
	}
	for _, name := range []string{"scan", "gogo", "neutron", "cyberhub"} {
		if profile.runtime.CommandRegistry().Has(name) {
			t.Fatalf("unexpected %s", name)
		}
	}
	session, err := profile.runtime.OpenSession(t.Context(), agentsession.SessionOptions{ID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Command(t.Context(), "/help"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	run, err := session.Run(ctx, agentsession.RunInput{Content: []*aop.Content{aop.Text("audit fixture")}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider never started")
	}
	cancel()
	_, err = run.Wait()
	if err == nil {
		t.Fatal("canceled run succeeded")
	}
	var docs strings.Builder
	_, err = profile.runtime.CommandRegistry().Execute(t.Context(), "proton", &coretool.Execution{Args: []string{"--template-list", "-c", "keys"}, Dir: workspace, Stdout: &docs, Stderr: io.Discard})
	if err != nil || !strings.Contains(docs.String(), "github") {
		t.Fatalf("proton: %v", err)
	}
}
func TestAuditDependencyBoundary(t *testing.T) {
	output, err := exec.Command("go", "list", "-deps", "../../cmd/cyber-audit").Output()
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"/pkg/exts/scanner", "/tools/scan", "/tools/resources", "/pkg/exts/web", "/pkg/exts/ioa", "/pkg/exts/browser", "/pkg/exts/search"}
	for _, dependency := range strings.Fields(string(output)) {
		for _, path := range forbidden {
			if strings.HasPrefix(dependency, "github.com/chainreactors/cyber"+path) {
				t.Errorf("audit depends on %s", dependency)
			}
		}
	}
}
