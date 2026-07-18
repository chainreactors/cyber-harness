package tui

import (
	"strings"
	"testing"

	outputpkg "github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent"
)

// assertUniformWidth checks every line of a rendered box has the same visible
// width — the box is a rectangle, nothing overflows the frame.
func assertUniformWidth(t *testing.T, box string) {
	t.Helper()
	lines := strings.Split(box, "\n")
	want := visibleWidth(lines[0])
	for i, ln := range lines {
		if got := visibleWidth(ln); got != want {
			t.Errorf("line %d width = %d, want %d: %q", i, got, want, outputpkg.StripANSI(ln))
		}
	}
}

// A long URL/path used to widen the whole box past the terminal. It must now be
// clipped so the frame stays exactly `width` columns.
func TestRenderFixedBoxNeverOverflows(t *testing.T) {
	const width = 60
	longURL := "http://" + strings.Repeat("a", 120) + "@127.0.0.1:3000/ioa"
	body := "status\nmodel   anthropic / glm-5.2\nioa     " + longURL
	box := renderFixedBox(body, width, false)
	for _, ln := range strings.Split(box, "\n") {
		if w := visibleWidth(ln); w != width {
			t.Fatalf("line width %d != box width %d: %q", w, width, ln)
		}
	}
}

// CJK runes are double-width; before the fix the right border drifted right under
// Chinese text because padding counted runes, not cells.
func TestRenderFixedBoxAlignsCJK(t *testing.T) {
	body := "状态\n模型    anthropic / glm-5.2\n技能    /aiscan /passive 中文说明文字很长很长很长"
	assertUniformWidth(t, renderFixedBox(body, 44, false))
}

func TestVisibleWidthCJK(t *testing.T) {
	if w := visibleWidth("中文"); w != 4 {
		t.Errorf("visibleWidth(中文) = %d, want 4", w)
	}
	if w := visibleWidth("\x1b[2mabc\x1b[0m"); w != 3 {
		t.Errorf("visibleWidth(colored abc) = %d, want 3", w)
	}
}

func TestClipVisiblePreservesANSIAndWidth(t *testing.T) {
	line := "\x1b[2mhttp://SECRETTOKEN@example.invalid/really/long/path/that/overflows\x1b[0m"
	out := clipVisible(line, 20)
	if w := visibleWidth(out); w > 20 {
		t.Errorf("clipVisible width = %d, want <= 20", w)
	}
	if !strings.HasPrefix(out, "\x1b[2m") {
		t.Errorf("clipVisible dropped opening color: %q", out)
	}
	if !strings.HasSuffix(out, outputpkg.ANSIReset) {
		t.Errorf("clipVisible left color open: %q", out)
	}
	if got := clipVisible("abc", 20); got != "abc" {
		t.Errorf("clipVisible(short) = %q, want abc", got)
	}
}

func TestRedactIOAURL(t *testing.T) {
	raw := "http://be7c1b68264bae5a37570e2785fea0725dd26760f49037d7081939178e81098f@127.0.0.1:3000/ioa"
	got := redactIOAURL(raw)
	if strings.Contains(got, "be7c1b68") {
		t.Errorf("redactIOAURL leaked token: %q", got)
	}
	if got != "http://127.0.0.1:3000/ioa" {
		t.Errorf("redactIOAURL = %q, want http://127.0.0.1:3000/ioa", got)
	}
	if got := redactIOAURL("http://127.0.0.1:3000/ioa"); got != "http://127.0.0.1:3000/ioa" {
		t.Errorf("redactIOAURL(no token) = %q", got)
	}
}

func TestRedactIOAURLFallbackOnMalformedURL(t *testing.T) {
	raw := "http://super-secret-token@127.0.0.1:3000/ioa/%zz"
	got := redactIOAURL(raw)
	if strings.Contains(got, "super-secret-token") {
		t.Fatalf("redactIOAURL malformed leaked token: %q", got)
	}
	if !strings.Contains(got, "127.0.0.1:3000") {
		t.Fatalf("redactIOAURL malformed dropped host: %q", got)
	}
}

func TestTruncMiddleKeepsTail(t *testing.T) {
	p := "/var/lib/cloud-cli-proxy/hosts/57dfa9df-9093-4bcb/home/aiscan/dist/.aiscan/agent_history"
	got := truncMiddle(p, 40)
	if n := visibleWidth(got); n > 40 {
		t.Errorf("truncMiddle width = %d, want <= 40", n)
	}
	if !strings.HasSuffix(got, "agent_history") {
		t.Errorf("truncMiddle dropped tail: %q", got)
	}
	if !strings.HasPrefix(got, "/var/lib") {
		t.Errorf("truncMiddle dropped head: %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("truncMiddle missing ellipsis: %q", got)
	}
}

func TestTruncMiddleUsesVisibleWidth(t *testing.T) {
	p := "/tmp/中文中文中文中文中文/agent_history"
	got := truncMiddle(p, 18)
	if n := visibleWidth(got); n > 18 {
		t.Fatalf("truncMiddle CJK width = %d, want <= 18: %q", n, got)
	}
	if !strings.HasSuffix(got, "history") {
		t.Fatalf("truncMiddle CJK dropped useful tail: %q", got)
	}
}

// The IOA list commands now render as boxed panels; columns must stay aligned
// (including under CJK names) and nothing overflows the frame.
func TestRenderBoxTableAligns(t *testing.T) {
	rows := [][]string{
		{"e8fa5859", "default", "1 node", "0 msgs"},
		{"de12abca", "作战一号-很长的中文名字", "3 nodes", "12 msgs"},
		{"b3a3e964", "local-1", "0 nodes", "0 msgs"},
	}
	box := renderFixedBox("spaces\n"+renderBoxTable(rows, false), 48, false)
	assertUniformWidth(t, box)
	t.Log("\n" + box)
}

func TestRenderBoxTableNodesSample(t *testing.T) {
	rows := [][]string{
		{"de12abca", "aiscan-tui"},
		{"b3a3e964", "local-1"},
	}
	box := renderFixedBox("nodes\n"+renderBoxTable(rows, false), 44, false)
	assertUniformWidth(t, box)
	t.Log("\n" + box)
}

func TestRenderBoxTableClipsWideIntermediateColumns(t *testing.T) {
	rows := [][]string{
		{"de12abca", strings.Repeat("非常长", 20), "3 nodes", "12 msgs"},
	}
	table := renderBoxTable(rows, false)
	if !strings.Contains(table, "12 msgs") {
		t.Fatalf("wide middle column hid the final column: %q", table)
	}
	if visibleWidth(strings.Split(table, "\n")[0]) > 80 {
		t.Fatalf("wide middle column was not clipped: width=%d table=%q", visibleWidth(table), table)
	}
}

func TestProviderModelDoesNotDependOnCommands(t *testing.T) {
	r := &AgentConsole{}
	r.appInfo.ProviderConfig = agent.ProviderConfig{Provider: "anthropic", Model: "claude-test"}
	provider, model := r.providerModel()
	if provider != "anthropic" || model != "claude-test" {
		t.Fatalf("providerModel = %q/%q, want anthropic/claude-test", provider, model)
	}
}

func TestShortID(t *testing.T) {
	if got := shortID("de12abca01d7a92f1630e21f642a37e0"); got != "de12abca" {
		t.Errorf("shortID = %q, want de12abca", got)
	}
	if got := shortID("abc"); got != "abc" {
		t.Errorf("shortID(short) = %q, want abc", got)
	}
}

// TestStatusSampleRender prints a realistic /status box (color off) so the fix
// is visible in `go test -v` output.
func TestStatusSampleRender(t *testing.T) {
	rows := []helpRow{
		{Command: "model", Detail: "anthropic / glm-5.2"},
		{Command: "render", Detail: "static · plain · space default"},
		{Command: "task", Detail: "idle"},
		{Command: "ioa", Detail: redactIOAURL("http://be7c1b68264bae5a37570e2785fea0725dd26760f49037d7081939178e81098f@127.0.0.1:3000/ioa") + " · space default"},
		{Command: "history", Detail: truncMiddle("/var/lib/cloud-cli-proxy/hosts/57dfa9df-9093-4bcb-80e8-bafaf96927ee/home/aiscan/dist/.aiscan/agent_history", 64-4-helpRowCommandWidth)},
		{Command: "skills", Detail: "/aiscan /passive"},
	}
	box := renderFixedBox("status\n"+renderHelpRows(rows, false), 64, false)
	t.Log("\n" + box)
	assertUniformWidth(t, box)
}
