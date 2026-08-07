//go:build full

package playwright

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/headless"
	"github.com/go-rod/rod/lib/launcher"
	"gopkg.in/yaml.v3"
)

func TestRecorderBasicActions(t *testing.T) {
	rec := newRecorder("https://example.com")

	rec.record(RecordedAction{
		Action: headless.ActionNavigate,
		Args:   map[string]string{"url": "{{BaseURL}}"},
	})
	rec.record(RecordedAction{
		Action: headless.ActionTextInput,
		Args:   map[string]string{"selector": "input[name=user]", "value": "admin"},
	})
	rec.record(RecordedAction{
		Action: headless.ActionClick,
		Args:   map[string]string{"selector": "button[type=submit]"},
	})

	if rec.len() != 3 {
		t.Fatalf("expected 3 actions, got %d", rec.len())
	}

	tmpl := rec.generateTemplate("test-login", "Login test")
	if tmpl == nil {
		t.Fatal("generateTemplate returned nil")
	}
	if tmpl.ID != "test-login" {
		t.Errorf("ID = %q, want %q", tmpl.ID, "test-login")
	}
	if len(tmpl.RequestsHeadless) != 1 {
		t.Fatalf("expected 1 request, got %d", len(tmpl.RequestsHeadless))
	}
	steps := tmpl.RequestsHeadless[0].Steps
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	if steps[0].ActionType.ActionType != headless.ActionNavigate {
		t.Errorf("step 0: got %v, want navigate", steps[0].ActionType)
	}
	if steps[1].ActionType.ActionType != headless.ActionTextInput {
		t.Errorf("step 1: got %v, want text", steps[1].ActionType)
	}
	if steps[2].ActionType.ActionType != headless.ActionClick {
		t.Errorf("step 2: got %v, want click", steps[2].ActionType)
	}
}

func TestRecorderTemplateURL(t *testing.T) {
	rec := newRecorder("https://example.com/app/login")

	tests := []struct {
		input string
		want  string
	}{
		{"https://example.com/app/login", "{{BaseURL}}/app/login"},
		{"https://example.com/other", "{{BaseURL}}/other"},
		{"https://other.com/path", "https://other.com/path"},
	}
	for _, tt := range tests {
		got := rec.templateURL(tt.input)
		if got != tt.want {
			t.Errorf("templateURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRecorderYAMLOutput(t *testing.T) {
	rec := newRecorder("https://example.com")
	rec.record(RecordedAction{
		Action: headless.ActionNavigate,
		Args:   map[string]string{"url": "{{BaseURL}}"},
	})
	rec.record(RecordedAction{
		Action: headless.ActionClick,
		Args:   map[string]string{"selector": "#login-btn"},
	})

	tmpl := rec.generateTemplate("click-test", "Click test")
	data, err := yaml.Marshal(tmpl)
	if err != nil {
		t.Fatal(err)
	}

	yamlStr := string(data)
	if !strings.Contains(yamlStr, "id: click-test") {
		t.Error("YAML missing template ID")
	}
	if !strings.Contains(yamlStr, "action: navigate") {
		t.Error("YAML missing navigate action")
	}
	if !strings.Contains(yamlStr, "action: click") {
		t.Error("YAML missing click action")
	}
	if !strings.Contains(yamlStr, "url: '{{BaseURL}}'") && !strings.Contains(yamlStr, `url: "{{BaseURL}}"`) && !strings.Contains(yamlStr, "url: '{{BaseURL}}'") {
		if !strings.Contains(yamlStr, "url:") {
			t.Error("YAML missing url arg")
		}
	}

	// Verify it can be parsed back by the headless engine.
	parsed, err := headless.ParseTemplate(data)
	if err != nil {
		t.Fatalf("round-trip parse failed: %v", err)
	}
	if parsed.ID != "click-test" {
		t.Errorf("round-trip ID = %q", parsed.ID)
	}
	if len(parsed.RequestsHeadless) != 1 || len(parsed.RequestsHeadless[0].Steps) != 2 {
		t.Errorf("round-trip steps count wrong")
	}
}

func TestRecordCommandMapping(t *testing.T) {
	sess := &Session{
		Name: "test",
		rec:  newRecorder("https://example.com"),
	}

	tests := []struct {
		cmd  string
		args []string
		want headless.ActionType
	}{
		{"click", []string{"test", "#btn"}, headless.ActionClick},
		{"fill", []string{"test", "input", "value"}, headless.ActionTextInput},
		{"press", []string{"test", "input", "Enter"}, headless.ActionKeyboard},
		{"select-option", []string{"test", "select", "opt1"}, headless.ActionSelectInput},
		{"screenshot", []string{"test"}, headless.ActionScreenshot},
		{"wait-for", []string{"test", "--stable"}, headless.ActionWaitStable},
		{"wait-for", []string{"test", "--idle"}, headless.ActionWaitIdle},
		{"wait-for", []string{"test", "#element"}, headless.ActionWaitVisible},
		{"hover", []string{"test", "#menu"}, headless.ActionHover},
		{"dblclick", []string{"test", "#item"}, headless.ActionDblClick},
		{"reload", []string{"test"}, headless.ActionReload},
		{"go-back", []string{"test"}, headless.ActionGoBack},
		{"go-forward", []string{"test"}, headless.ActionGoForward},
		{"dialog", []string{"test", "--arm"}, headless.ActionDialog},
		{"text-content", []string{"test", "#result"}, headless.ActionExtract},
		{"get-attribute", []string{"test", "a", "href"}, headless.ActionExtract},
		{"inner-text", []string{"test", "#text"}, headless.ActionExtract},
		{"inner-html", []string{"test", "#markup"}, headless.ActionExtract},
		{"check", []string{"test", "#terms"}, headless.ActionCheck},
		{"uncheck", []string{"test", "#terms"}, headless.ActionUncheck},
		{"focus", []string{"test", "#email"}, headless.ActionFocus},
		{"blur", []string{"test", "#email"}, headless.ActionBlur},
		{"dispatch-event", []string{"test", "#form", "submit"}, headless.ActionDispatchEvent},
		{"set-viewport", []string{"test", "1280", "720"}, headless.ActionSetViewport},
		{"wait-for-url", []string{"test", "/done"}, headless.ActionWaitURL},
		{"wait-for-request", []string{"test", "/api/start"}, headless.ActionWaitRequest},
		{"wait-for-response", []string{"test", "/api/result"}, headless.ActionWaitResponse},
		{"set-content", []string{"test", "<main>ready</main>"}, headless.ActionSetContent},
		{"localstorage-set", []string{"test", "token", "abc"}, headless.ActionStorage},
		{"sessionstorage-delete", []string{"test", "draft"}, headless.ActionStorage},
		{"cookie-set", []string{"test", "sid=123"}, headless.ActionCookie},
		{"cookie-delete", []string{"test", "sid"}, headless.ActionCookie},
		{"is-visible", []string{"test", "#ready"}, headless.ActionAssert},
	}

	for _, tt := range tests {
		before := sess.rec.len()
		ok := recordCommand(sess, tt.cmd, tt.args)
		if !ok {
			t.Errorf("recordCommand(%q) returned false", tt.cmd)
			continue
		}
		actions := sess.rec.snapshot()
		last := actions[len(actions)-1]
		if last.Action != tt.want {
			t.Errorf("recordCommand(%q): got action %v, want %v", tt.cmd, last.Action, tt.want)
		}
		_ = before
	}
}

func TestRecordCommandReplaySemantics(t *testing.T) {
	sess := &Session{Name: "test", rec: newRecorder("https://example.com")}

	recordCommand(sess, "fill", []string{"test", "label=Email", "alice@example.com"})
	recordCommand(sess, "press", []string{"test", `role=button[name="Sign in"]`, "Shift+Enter"})
	recordCommand(sess, "wait-for", []string{"test", "testid=ready"})

	actions := sess.rec.snapshot()
	if len(actions) != 3 {
		t.Fatalf("expected 3 actions, got %d", len(actions))
	}
	if actions[0].Args["clear"] != "true" || actions[0].Args["by"] != "label" || actions[0].Args["label"] != "Email" {
		t.Fatalf("fill did not preserve clear/label semantics: %#v", actions[0].Args)
	}
	if actions[1].Args["by"] != "role" || actions[1].Args["role"] != "button" || actions[1].Args["name"] != "Sign in" {
		t.Fatalf("press did not preserve role selector: %#v", actions[1].Args)
	}
	if actions[1].Args["keys"] != "Shift+Enter" {
		t.Fatalf("press keys = %q", actions[1].Args["keys"])
	}
	if actions[2].Args["by"] != "testid" || actions[2].Args["testid"] != "ready" {
		t.Fatalf("wait selector was not parsed semantically: %#v", actions[2].Args)
	}
}

func TestRecordCommandResultPreservesBooleanState(t *testing.T) {
	sess := &Session{Name: "test", rec: newRecorder("https://example.com")}
	if !recordCommandResult(sess, "is-visible", []string{"test", "#optional"}, "#optional visible = false") {
		t.Fatal("is-visible result was not recorded")
	}
	if got := sess.rec.snapshot()[0].Args["type"]; got != "hidden" {
		t.Fatalf("false is-visible result recorded as %q, want hidden", got)
	}
}

func TestRecordCommandXPath(t *testing.T) {
	sess := &Session{
		Name: "test",
		rec:  newRecorder("https://example.com"),
	}

	recordCommand(sess, "click", []string{"test", "xpath://div[@id='login']"})
	actions := sess.rec.snapshot()
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	if actions[0].Args["by"] != "xpath" {
		t.Errorf("expected by=xpath, got %q", actions[0].Args["by"])
	}
	if actions[0].Args["xpath"] != "//div[@id='login']" {
		t.Errorf("expected xpath value, got %q", actions[0].Args["xpath"])
	}
}

func TestRecorderEmpty(t *testing.T) {
	rec := newRecorder("https://example.com")
	if tmpl := rec.generateTemplate("empty", "Empty"); tmpl != nil {
		t.Error("expected nil template for empty recorder")
	}
}

func TestRecordSetExtraHeaders(t *testing.T) {
	sess := &Session{
		Name: "test",
		rec:  newRecorder("https://example.com"),
	}

	ok := recordCommand(sess, "set-extra-headers", []string{"test", `{"Authorization":"Bearer token","X-Custom":"value"}`})
	if !ok {
		t.Fatal("recordCommand returned false for set-extra-headers")
	}

	actions := sess.rec.snapshot()
	if len(actions) != 2 {
		t.Fatalf("expected 2 setheader actions, got %d", len(actions))
	}
	for _, a := range actions {
		if a.Action != headless.ActionSetHeader {
			t.Errorf("expected setheader, got %v", a.Action)
		}
	}
}

// ---------------------------------------------------------------------------
// Integration tests (require browser)
// ---------------------------------------------------------------------------

func skipIfNoBrowserR(t *testing.T) {
	t.Helper()
	if _, exists := launcher.LookPath(); !exists {
		t.Skip("no Chromium/Chrome found, skipping browser integration test")
	}
}

func loginTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/", "/login":
			fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>Login Page</title></head>
<body>
  <h1>Login</h1>
  <form id="login-form" action="/dashboard" method="POST">
    <input type="text" name="username" id="username" placeholder="Username">
    <input type="password" name="password" id="password" placeholder="Password">
    <select name="role" id="role">
      <option value="user">User</option>
      <option value="admin">Admin</option>
    </select>
    <button type="submit" id="submit-btn">Login</button>
  </form>
  <a href="/about" id="about-link">About</a>
  <div id="version">v1.0.0</div>
</body>
</html>`)
		case "/dashboard":
			fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>Dashboard</title></head>
<body>
  <h1>Welcome admin</h1>
  <div id="status">Logged in successfully</div>
  <button id="logout" onclick="alert('Logging out')">Logout</button>
</body>
</html>`)
		default:
			w.WriteHeader(404)
			fmt.Fprint(w, "not found")
		}
	}))
}

// recExecString is a test helper that runs cmd.Execute and returns the output as a string.
func recExecString(t *testing.T, cmd *Command, ctx context.Context, args []string) string {
	t.Helper()
	var output bytes.Buffer
	if _, err := cmd.Run(ctx, &commands.Execution{Args: args, Stdout: &output, Stderr: &output}); err != nil {
		t.Fatalf("Execute(%v) error = %v", args, err)
	}
	return output.String()
}

// recExecStringErr is a test helper that runs cmd.Execute and returns (output, error).
func recExecStringErr(cmd *Command, ctx context.Context, args []string) (string, error) {
	var output bytes.Buffer
	_, err := cmd.Run(ctx, &commands.Execution{Args: args, Stdout: &output, Stderr: &output})
	return output.String(), err
}

// TestIntegration_RecordOpenWithFlag tests --record flag on open.
func TestIntegration_RecordOpenWithFlag(t *testing.T) {
	skipIfNoBrowserR(t)

	srv := loginTestServer()
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()

	// Open with --record
	out := recExecString(t, cmd, context.Background(), []string{
		"open", srv.URL + "/login", "--session", "rec1", "--record", "--timeout", "10",
	})
	if !strings.Contains(out, "Recording: on") {
		t.Fatalf("expected 'Recording: on' in output, got:\n%s", out)
	}

	// Verify initial navigate was auto-recorded
	out = recExecString(t, cmd, context.Background(), []string{"record", "rec1", "--dump"})
	if !strings.Contains(out, "action: navigate") {
		t.Fatalf("expected navigate in dump, got:\n%s", out)
	}
	if !strings.Contains(out, "{{BaseURL}}") {
		t.Fatalf("expected {{BaseURL}} in dump, got:\n%s", out)
	}
}

// TestIntegration_RecordFullLoginFlow tests a complete login flow recording.
func TestIntegration_RecordFullLoginFlow(t *testing.T) {
	skipIfNoBrowserR(t)

	srv := loginTestServer()
	defer srv.Close()

	workDir := t.TempDir()
	cmd := New(workDir)
	defer cmd.Close()

	ctx := context.Background()

	// Open with recording
	recExecString(t, cmd, ctx, []string{
		"open", srv.URL + "/login", "--session", "login", "--record", "--timeout", "10",
	})

	// Fill username
	recExecString(t, cmd, ctx, []string{"fill", "login", "#username", "admin"})

	// Fill password
	recExecString(t, cmd, ctx, []string{"fill", "login", "#password", "secret123"})

	// Select role
	if _, err := cmd.Run(ctx, &commands.Execution{Args: []string{"select-option", "login", "#role", "admin"}, Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		// select might fail depending on rod version, skip if error
		t.Logf("select-option skipped: %v", err)
	}

	// Click submit
	recExecString(t, cmd, ctx, []string{"click", "login", "#submit-btn"})

	// Wait for page stable
	recExecString(t, cmd, ctx, []string{"wait", "login", "--stable"})

	// Extract text
	if _, err := cmd.Run(ctx, &commands.Execution{Args: []string{"text-content", "login", "#status"}, Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		t.Logf("text-content skipped: %v", err)
	}

	// Dump recorded YAML
	out := recExecString(t, cmd, ctx, []string{"record", "login", "--dump"})

	t.Logf("=== Recorded YAML ===\n%s", out)

	// Verify all expected actions are present
	expected := []string{
		"action: navigate",
		"action: text",
		"action: click",
		"action: waitstable",
	}
	for _, exp := range expected {
		if !strings.Contains(out, exp) {
			t.Errorf("dump missing %q", exp)
		}
	}

	// Verify args are correct
	if !strings.Contains(out, "admin") {
		t.Error("dump missing username 'admin'")
	}
	if !strings.Contains(out, "secret123") {
		t.Error("dump missing password 'secret123'")
	}
	if !strings.Contains(out, "#username") {
		t.Error("dump missing selector '#username'")
	}
	if !strings.Contains(out, "#submit-btn") {
		t.Error("dump missing selector '#submit-btn'")
	}

	// Save to file
	outPath := filepath.Join(workDir, "login-poc.yaml")
	saveOut := recExecString(t, cmd, ctx, []string{
		"record", "login", "--save", outPath,
		"--id", "login-bypass",
		"--name", "Login bypass POC",
	})
	if !strings.Contains(saveOut, "Template saved") {
		t.Fatalf("expected 'Template saved' in output, got:\n%s", saveOut)
	}

	// Verify file exists and is valid
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read saved template: %v", err)
	}
	t.Logf("=== Saved Template ===\n%s", string(data))

	// Parse with headless engine to verify compatibility
	tmpl, err := headless.ParseTemplate(data)
	if err != nil {
		t.Fatalf("headless.ParseTemplate failed: %v", err)
	}
	if tmpl.ID != "login-bypass" {
		t.Errorf("template ID = %q, want 'login-bypass'", tmpl.ID)
	}
	if tmpl.Info.Name != "Login bypass POC" {
		t.Errorf("template name = %q", tmpl.Info.Name)
	}
	if len(tmpl.RequestsHeadless) != 1 {
		t.Fatalf("expected 1 request, got %d", len(tmpl.RequestsHeadless))
	}

	steps := tmpl.RequestsHeadless[0].Steps
	t.Logf("Parsed %d steps from saved template", len(steps))
	for i, s := range steps {
		t.Logf("  step %d: %s %v", i, s.ActionType.String(), s.Data)
	}

	if len(steps) < 4 {
		t.Fatalf("expected at least 4 steps, got %d", len(steps))
	}
	if steps[0].ActionType.ActionType != headless.ActionNavigate {
		t.Errorf("step 0 should be navigate, got %v", steps[0].ActionType)
	}
}

// TestIntegration_RecordStartStop tests the record --start / --stop flow.
func TestIntegration_RecordStartStop(t *testing.T) {
	skipIfNoBrowserR(t)

	srv := loginTestServer()
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()
	ctx := context.Background()

	// Open without --record
	out := recExecString(t, cmd, ctx, []string{
		"open", srv.URL + "/login", "--session", "s2", "--timeout", "10",
	})
	if !strings.Contains(out, "Recording: off") {
		t.Fatalf("expected 'Recording: off' in output, got:\n%s", out)
	}

	// record --dump should fail when not recording
	_, err := recExecStringErr(cmd, ctx, []string{"record", "s2", "--dump"})
	if err == nil {
		t.Fatal("expected error for dump without recording")
	}

	// Start recording
	out = recExecString(t, cmd, ctx, []string{"record", "s2", "--start"})
	if !strings.Contains(out, "Recording started") {
		t.Fatalf("expected 'Recording started', got:\n%s", out)
	}

	// Do some actions
	if _, err := cmd.Run(ctx, &commands.Execution{Args: []string{"click", "s2", "#about-link"}, Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		t.Logf("click about link: %v (continuing)", err)
	}

	// Dump should now work
	out = recExecString(t, cmd, ctx, []string{"record", "s2", "--dump"})
	if !strings.Contains(out, "action: click") {
		t.Logf("dump output: %s", out)
	}

	// Stop recording
	out = recExecString(t, cmd, ctx, []string{"record", "s2", "--stop"})
	if !strings.Contains(out, "Recording stopped") {
		t.Fatalf("expected 'Recording stopped', got:\n%s", out)
	}

	// Dump should fail again after stop
	_, err = recExecStringErr(cmd, ctx, []string{"record", "s2", "--dump"})
	if err == nil {
		t.Fatal("expected error for dump after stop")
	}
}

// TestIntegration_RecordClear tests the record --clear flow.
func TestIntegration_RecordClear(t *testing.T) {
	skipIfNoBrowserR(t)

	srv := loginTestServer()
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()
	ctx := context.Background()

	recExecString(t, cmd, ctx, []string{
		"open", srv.URL + "/login", "--session", "s3", "--record", "--timeout", "10",
	})

	// Should have 1 action (navigate)
	recExecString(t, cmd, ctx, []string{"click", "s3", "#submit-btn"})

	// Clear
	out := recExecString(t, cmd, ctx, []string{"record", "s3", "--clear"})
	if !strings.Contains(out, "Recording cleared") {
		t.Fatalf("expected 'Recording cleared', got:\n%s", out)
	}

	// Dump should show "No actions recorded"
	out = recExecString(t, cmd, ctx, []string{"record", "s3", "--dump"})
	if !strings.Contains(out, "No actions recorded") {
		t.Fatalf("expected 'No actions recorded', got:\n%s", out)
	}
}

// TestIntegration_RecordXPath tests recording with xpath selectors.
func TestIntegration_RecordXPath(t *testing.T) {
	skipIfNoBrowserR(t)

	srv := loginTestServer()
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()
	ctx := context.Background()

	recExecString(t, cmd, ctx, []string{
		"open", srv.URL + "/login", "--session", "xpath", "--record", "--timeout", "10",
	})

	// Use xpath selector
	recExecString(t, cmd, ctx, []string{"click", "xpath", "xpath://button[@id='submit-btn']"})

	out := recExecString(t, cmd, ctx, []string{"record", "xpath", "--dump"})

	// Verify xpath is recorded correctly
	if !strings.Contains(out, "by: xpath") {
		t.Errorf("expected 'by: xpath' in dump, got:\n%s", out)
	}
	if !strings.Contains(out, "xpath: //button[@id='submit-btn']") {
		t.Errorf("expected xpath value in dump, got:\n%s", out)
	}
}

// TestIntegration_RecordExtract tests recording extraction commands.
func TestIntegration_RecordExtract(t *testing.T) {
	skipIfNoBrowserR(t)

	srv := loginTestServer()
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()
	ctx := context.Background()

	recExecString(t, cmd, ctx, []string{
		"open", srv.URL + "/login", "--session", "ext", "--record", "--timeout", "10",
	})

	// Extract text content
	recExecString(t, cmd, ctx, []string{"text-content", "ext", "#version"})

	// Get attribute
	recExecString(t, cmd, ctx, []string{"get-attribute", "ext", "#about-link", "href"})

	out := recExecString(t, cmd, ctx, []string{"record", "ext", "--dump"})

	t.Logf("=== Extract Recording ===\n%s", out)

	// Should have extract actions with names
	if !strings.Contains(out, "action: extract") {
		t.Error("expected extract action in dump")
	}
	if !strings.Contains(out, "name:") {
		t.Error("expected named extractions in dump")
	}
}

// TestIntegration_RecordEval tests recording JS evaluation.
func TestIntegration_RecordEval(t *testing.T) {
	skipIfNoBrowserR(t)

	srv := loginTestServer()
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()
	ctx := context.Background()

	recExecString(t, cmd, ctx, []string{
		"open", srv.URL + "/login", "--session", "js", "--record", "--timeout", "10",
	})

	// Run JS
	recExecString(t, cmd, ctx, []string{"evaluate", "js", "document.title"})

	out := recExecString(t, cmd, ctx, []string{"record", "js", "--dump"})

	if !strings.Contains(out, "action: script") {
		t.Errorf("expected script action in dump, got:\n%s", out)
	}
	if !strings.Contains(out, "document.title") {
		t.Errorf("expected JS code in dump, got:\n%s", out)
	}
}

// TestIntegration_RecordSessionsList tests sessions list with recording indicator.
func TestIntegration_RecordSessionsList(t *testing.T) {
	skipIfNoBrowserR(t)

	srv := loginTestServer()
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()
	ctx := context.Background()

	recExecString(t, cmd, ctx, []string{
		"open", srv.URL + "/login", "--session", "r1", "--record", "--timeout", "10",
	})

	out := recExecString(t, cmd, ctx, []string{"sessions"})

	// Should show recording indicator
	if !strings.Contains(out, "rec=") {
		t.Fatalf("expected 'rec=' in sessions output, got:\n%s", out)
	}
}

// TestIntegration_RecordCloseWarning tests that closing with unsaved recording shows warning.
func TestIntegration_RecordCloseWarning(t *testing.T) {
	skipIfNoBrowserR(t)

	srv := loginTestServer()
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()
	ctx := context.Background()

	recExecString(t, cmd, ctx, []string{
		"open", srv.URL + "/login", "--session", "warn", "--record", "--timeout", "10",
	})

	// Close without saving
	out := recExecString(t, cmd, ctx, []string{"close", "warn"})

	if !strings.Contains(out, "recorded actions not saved") {
		t.Fatalf("expected unsaved recording warning, got:\n%s", out)
	}
}

// TestIntegration_RecordRoundTrip tests the full record -> save -> parse -> execute cycle.
func TestIntegration_RecordRoundTrip(t *testing.T) {
	skipIfNoBrowserR(t)

	srv := loginTestServer()
	defer srv.Close()

	workDir := t.TempDir()
	cmd := New(workDir)
	defer cmd.Close()
	ctx := context.Background()

	// Step 1: Record
	recExecString(t, cmd, ctx, []string{
		"open", srv.URL + "/login", "--session", "rt", "--record", "--timeout", "10",
	})

	recExecString(t, cmd, ctx, []string{"fill", "rt", "#username", "testuser"})
	recExecString(t, cmd, ctx, []string{"click", "rt", "#submit-btn"})
	recExecString(t, cmd, ctx, []string{"wait", "rt", "--stable"})

	// Step 2: Save
	templatePath := filepath.Join(workDir, "roundtrip.yaml")
	recExecString(t, cmd, ctx, []string{
		"record", "rt", "--save", templatePath, "--id", "roundtrip-test",
	})

	// Step 3: Parse with headless engine
	data, _ := os.ReadFile(templatePath)
	t.Logf("=== Saved Template ===\n%s", string(data))

	tmpl, err := headless.ParseTemplate(data)
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	if tmpl.ID != "roundtrip-test" {
		t.Errorf("template ID = %q", tmpl.ID)
	}

	steps := tmpl.RequestsHeadless[0].Steps
	t.Logf("Round-trip: %d steps parsed", len(steps))

	// Verify action sequence
	actionTypes := make([]string, len(steps))
	for i, s := range steps {
		actionTypes[i] = s.ActionType.String()
	}
	t.Logf("Actions: %v", actionTypes)

	if steps[0].ActionType.ActionType != headless.ActionNavigate {
		t.Error("first step should be navigate")
	}

	hasText := false
	hasClick := false
	hasWaitStable := false
	for _, s := range steps {
		switch s.ActionType.ActionType {
		case headless.ActionTextInput:
			hasText = true
			if s.GetArg("value") != "testuser" {
				t.Errorf("text input value = %q, want 'testuser'", s.GetArg("value"))
			}
		case headless.ActionClick:
			hasClick = true
		case headless.ActionWaitStable:
			hasWaitStable = true
		}
	}
	if !hasText {
		t.Error("missing text input step")
	}
	if !hasClick {
		t.Error("missing click step")
	}
	if !hasWaitStable {
		t.Error("missing waitstable step")
	}

	// Step 4: Execute the generated template with playwright template command
	out := recExecString(t, cmd, ctx, []string{
		"template", templatePath, srv.URL + "/login",
	})
	t.Logf("=== Template Execution ===\n%s", out)

	if !strings.Contains(out, "Template: roundtrip-test") {
		t.Errorf("expected template ID in output, got:\n%s", out)
	}
}

func TestIntegration_RecordExtendedRoundTrip(t *testing.T) {
	skipIfNoBrowserR(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><body>
<label for="email">Email</label><input id="email" value="old">
<label><input type="checkbox" data-testid="terms"> Accept terms</label>
<select aria-label="Plan"><option value="free">Free</option><option value="pro">Professional</option></select>
<button id="continue">Continue</button>
<script>document.getElementById('continue').addEventListener('aiscan', () => document.body.dataset.event = 'seen')</script>
</body></html>`)
	}))
	defer srv.Close()

	workDir := t.TempDir()
	cmd := New(workDir)
	defer cmd.Close()
	ctx := context.Background()

	recExecString(t, cmd, ctx, []string{"open", srv.URL, "--session", "extended", "--record", "--timeout", "10"})
	recExecString(t, cmd, ctx, []string{"fill", "extended", "label=Email", "alice@example.com"})
	recExecString(t, cmd, ctx, []string{"press", "extended", "label=Email", "End"})
	recExecString(t, cmd, ctx, []string{"check", "extended", "testid=terms"})
	recExecString(t, cmd, ctx, []string{"select-option", "extended", `role=combobox[name="Plan"]`, "pro"})
	recExecString(t, cmd, ctx, []string{"hover", "extended", `role=button[name="Continue"]`})
	recExecString(t, cmd, ctx, []string{"dblclick", "extended", `role=button[name="Continue"]`})
	recExecString(t, cmd, ctx, []string{"dispatch-event", "extended", "#continue", "aiscan"})
	recExecString(t, cmd, ctx, []string{"localstorage-set", "extended", "token", "abc123"})
	recExecString(t, cmd, ctx, []string{"cookie-set", "extended", "session=cookie-value"})
	recExecString(t, cmd, ctx, []string{"set-viewport", "extended", "1024", "768"})
	recExecString(t, cmd, ctx, []string{"is-checked", "extended", "testid=terms"})

	templatePath := filepath.Join(workDir, "extended-roundtrip.yaml")
	recExecString(t, cmd, ctx, []string{"record", "extended", "--save", templatePath, "--id", "extended-roundtrip"})
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := headless.ParseTemplate(data)
	if err != nil {
		t.Fatalf("parse recorded template: %v", err)
	}

	wantActions := map[headless.ActionType]bool{
		headless.ActionTextInput: false, headless.ActionKeyboard: false,
		headless.ActionCheck: false, headless.ActionSelectInput: false,
		headless.ActionHover: false, headless.ActionDblClick: false,
		headless.ActionDispatchEvent: false, headless.ActionStorage: false,
		headless.ActionCookie: false, headless.ActionSetViewport: false,
		headless.ActionAssert: false,
	}
	for _, step := range tmpl.RequestsHeadless[0].Steps {
		if _, tracked := wantActions[step.ActionType.ActionType]; tracked {
			wantActions[step.ActionType.ActionType] = true
		}
	}
	for actionType, found := range wantActions {
		if !found {
			t.Errorf("recorded template missing %s action", actionType)
		}
	}

	out := recExecString(t, cmd, ctx, []string{"template", templatePath, srv.URL})
	if !strings.Contains(out, "Template: extended-roundtrip") {
		t.Fatalf("extended template did not replay: %s", out)
	}
}

func TestE2E_RecordReplayAuthenticatedDashboard(t *testing.T) {
	skipIfNoBrowserR(t)

	type loginRequest struct {
		Email     string `json:"email"`
		Password  string `json:"password"`
		Workspace string `json:"workspace"`
		Remember  bool   `json:"remember"`
	}

	var (
		loginMu       sync.Mutex
		loginRequests []loginRequest
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/signin", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><title>Acme Cloud Sign In</title></head><body>
<main>
  <h1>Sign in to Acme Cloud</h1>
  <form id="signin-form">
    <label for="email">Work email</label><input id="email" name="email" type="email" value="stale@example.test">
    <label for="password">Password</label><input id="password" name="password" type="password">
    <label for="workspace">Workspace</label>
    <select id="workspace" name="workspace" aria-label="Workspace">
      <option value="engineering">Engineering</option><option value="security">Security</option>
    </select>
    <label><input id="remember" type="checkbox" data-testid="remember-device"> Remember this device</label>
    <button type="submit">Sign in</button>
    <p id="status" role="status"></p>
  </form>
</main>
<script>
document.getElementById('signin-form').addEventListener('submit', event => {
  event.preventDefault();
  const button = event.submitter;
  button.disabled = true;
  document.getElementById('status').textContent = 'Signing in...';
  window.setTimeout(async () => {
    const response = await fetch('/api/login', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({
        email: document.getElementById('email').value,
        password: document.getElementById('password').value,
        workspace: document.getElementById('workspace').value,
        remember: document.getElementById('remember').checked
      })
    });
    if (!response.ok) {
      document.getElementById('status').textContent = 'Sign in failed';
      button.disabled = false;
      return;
    }
    const session = await response.json();
    localStorage.setItem('lastWorkspace', session.workspace);
    window.location.assign('/app/dashboard?workspace=' + encodeURIComponent(session.workspace));
  }, 750);
});
</script></body></html>`)
	})
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var payload loginRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		loginMu.Lock()
		loginRequests = append(loginRequests, payload)
		loginMu.Unlock()
		if payload.Email != "analyst@example.test" || payload.Password != "correct-horse" || payload.Workspace != "security" || !payload.Remember {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "auth_session", Value: "session-e2e", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"workspace":"security"}`)
	})
	mux.HandleFunc("/app/dashboard", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("auth_session")
		if err != nil || cookie.Value != "session-e2e" {
			http.Redirect(w, r, "/signin", http.StatusFound)
			return
		}
		workspace := r.URL.Query().Get("workspace")
		if workspace == "" {
			workspace = "unknown"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html><head><title>Security Dashboard</title></head><body>
<nav aria-label="Primary"><a href="/app/dashboard">Overview</a></nav>
<main><h1>%s dashboard</h1><p data-testid="welcome">Welcome, analyst@example.test</p>
<p data-testid="session-status">Authenticated session</p><button>Sign out</button></main>
</body></html>`, strings.ToUpper(workspace[:1])+workspace[1:])
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	workDir := t.TempDir()
	cmd := New(workDir)
	defer cmd.Close()
	ctx := context.Background()

	recExecString(t, cmd, ctx, []string{"open", srv.URL + "/signin", "--session", "auth", "--record", "--timeout", "10"})
	recExecString(t, cmd, ctx, []string{"fill", "auth", "label=Work email", "analyst@example.test"})
	recExecString(t, cmd, ctx, []string{"fill", "auth", "label=Password", "correct-horse"})
	recExecString(t, cmd, ctx, []string{"select-option", "auth", `role=combobox[name="Workspace"]`, "security"})
	recExecString(t, cmd, ctx, []string{"check", "auth", "testid=remember-device"})
	recExecString(t, cmd, ctx, []string{"click", "auth", `role=button[name="Sign in"]`})
	recExecString(t, cmd, ctx, []string{"wait-for-response", "auth", "/api/login"})
	recExecString(t, cmd, ctx, []string{"wait-for-url", "auth", "/app/dashboard"})
	welcome := recExecString(t, cmd, ctx, []string{"inner-text", "auth", "testid=welcome"})
	if !strings.Contains(welcome, "Welcome, analyst@example.test") {
		t.Fatalf("dashboard did not render signed-in user: %s", welcome)
	}
	recExecString(t, cmd, ctx, []string{"is-visible", "auth", `role=heading[name="Security dashboard"]`})
	recExecString(t, cmd, ctx, []string{"is-visible", "auth", "testid=session-status"})
	recExecString(t, cmd, ctx, []string{"is-enabled", "auth", `role=button[name="Sign out"]`})
	storage := recExecString(t, cmd, ctx, []string{"localstorage-get", "auth", "lastWorkspace"})
	if !strings.Contains(storage, "security") {
		t.Fatalf("dashboard storage state missing: %s", storage)
	}
	cookie := recExecString(t, cmd, ctx, []string{"cookie-get", "auth", "auth_session"})
	if !strings.Contains(cookie, "session-e2e") {
		t.Fatalf("authenticated cookie missing: %s", cookie)
	}

	templatePath := filepath.Join(workDir, "authenticated-dashboard.yaml")
	recExecString(t, cmd, ctx, []string{"record", "auth", "--save", templatePath, "--id", "authenticated-dashboard-e2e"})
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := headless.ParseTemplate(data)
	if err != nil {
		t.Fatalf("parse recorded template: %v", err)
	}
	if len(tmpl.RequestsHeadless) != 1 {
		t.Fatalf("recorded template has %d headless requests", len(tmpl.RequestsHeadless))
	}

	wantActions := map[headless.ActionType]bool{
		headless.ActionNavigate: false, headless.ActionTextInput: false,
		headless.ActionSelectInput: false, headless.ActionCheck: false,
		headless.ActionClick: false, headless.ActionWaitResponse: false,
		headless.ActionWaitURL: false, headless.ActionAssert: false,
		headless.ActionExtract: false,
	}
	for _, step := range tmpl.RequestsHeadless[0].Steps {
		if _, tracked := wantActions[step.ActionType.ActionType]; tracked {
			wantActions[step.ActionType.ActionType] = true
		}
		if step.ActionType.ActionType == headless.ActionNavigate && strings.Contains(step.GetArg("url"), srv.URL) {
			t.Fatalf("recorded navigation leaked the original origin: %s", step.GetArg("url"))
		}
	}
	for actionType, found := range wantActions {
		if !found {
			t.Errorf("recorded authenticated flow missing %s action", actionType)
		}
	}

	out := recExecString(t, cmd, ctx, []string{"template", templatePath, srv.URL + "/signin"})
	if !strings.Contains(out, "Template: authenticated-dashboard-e2e") {
		t.Fatalf("authenticated template did not replay: %s", out)
	}

	loginMu.Lock()
	requests := append([]loginRequest(nil), loginRequests...)
	loginMu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("login API received %d requests, want one live and one replay request", len(requests))
	}
	for index, request := range requests {
		if request.Email != "analyst@example.test" || request.Password != "correct-horse" || request.Workspace != "security" || !request.Remember {
			t.Errorf("login request %d was not reproduced: %#v", index, request)
		}
	}
}
