//go:build browser

package playwright

import (
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/headless"
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
		{"hover", []string{"test", "#menu"}, headless.ActionScript},
		{"dblclick", []string{"test", "#item"}, headless.ActionScript},
		{"reload", []string{"test"}, headless.ActionScript},
		{"go-back", []string{"test"}, headless.ActionScript},
		{"dialog", []string{"test", "--arm"}, headless.ActionWaitDialog},
		{"text-content", []string{"test", "#result"}, headless.ActionExtract},
		{"get-attribute", []string{"test", "a", "href"}, headless.ActionExtract},
		{"inner-text", []string{"test", "#text"}, headless.ActionExtract},
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
