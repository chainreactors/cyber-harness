package commands_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"

	tmux "github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/commands"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// simpleCommand is a minimal Command implementation for scanner tests.
type simpleCommand struct{ name string }

func (c *simpleCommand) Name() string  { return c.name }
func (c *simpleCommand) Usage() string { return c.name }
func (c *simpleCommand) Execute(_ context.Context, _ []string) error {
	fmt.Fprint(commands.Output, "ok")
	return nil
}

// argsCapture records the args received by Execute.
type argsCapture struct {
	name string
	got  []string
}

func (c *argsCapture) Name() string  { return c.name }
func (c *argsCapture) Usage() string { return c.name }
func (c *argsCapture) Execute(_ context.Context, args []string) error {
	c.got = append([]string(nil), args...)
	fmt.Fprint(commands.Output, strings.Join(args, " "))
	return nil
}

// outputCommand writes multi-line output to commands.Output, simulating a
// pseudo-command that produces filterable results.
type outputCommand struct {
	name   string
	output string
}

func (c *outputCommand) Name() string  { return c.name }
func (c *outputCommand) Usage() string { return c.name + " — test command" }
func (c *outputCommand) Execute(_ context.Context, _ []string) error {
	_, err := commands.Output.Write([]byte(c.output))
	return err
}

// panicTool is a test tool that always panics.
type panicTool struct{ msg string }

func (t *panicTool) Name() string                          { return "panic_tool" }
func (t *panicTool) Description() string                   { return "always panics" }
func (t *panicTool) Definition() commands.ToolDefinition   { return commands.ToolDefinition{} }
func (t *panicTool) Execute(_ context.Context, _ string) (commands.ToolResult, error) {
	panic(t.msg)
}

// normalTool returns a result without panicking.
type normalTool struct{}

func (t *normalTool) Name() string                          { return "normal_tool" }
func (t *normalTool) Description() string                   { return "works fine" }
func (t *normalTool) Definition() commands.ToolDefinition   { return commands.ToolDefinition{} }
func (t *normalTool) Execute(_ context.Context, _ string) (commands.ToolResult, error) {
	return commands.TextResult("hello"), nil
}

func bashArgs(cmd string) string {
	data, _ := json.Marshal(map[string]string{"command": cmd})
	return string(data)
}

func newBashWithPseudo(dir string, cmds ...*outputCommand) *commands.BashTool {
	registry := commands.NewRegistry()
	for _, c := range cmds {
		registry.Register(c, "")
	}
	bash := commands.NewBashTool(dir, 10)
	bash.Manager().SetCommands(func(name string) (tmux.Command, bool) {
		return registry.Get(name)
	})
	bash.Manager().SetExecHooks(
		func(w io.Writer) { commands.Output.Reset(w) },
		func() { commands.Output.Reset(nil) },
	)
	bash.Manager().SetWorkDir(dir)
	return bash
}

// ---------------------------------------------------------------------------
// Scanner tests (from bash_scanner_test.go)
// ---------------------------------------------------------------------------

func TestScannerRejectsShellPipeAndFileRedir(t *testing.T) {
	registry := commands.NewRegistry()
	registry.Register(&simpleCommand{name: "spray"}, "")
	bash := commands.NewBashTool(t.TempDir(), 5)
	bash.Manager().SetCommands(func(name string) (tmux.Command, bool) {
		return registry.Get(name)
	})

	// Single pipe (|) is now supported — pseudo-command output is piped
	// through a shell pipeline. Only ||, redirections, and chaining are
	// still rejected.
	tests := []struct {
		name     string
		cmd      string
		wantHint string
	}{
		{"double pipe", `spray -u http://x || echo done`, "shell pipes"},
		{"file redirection >", `spray -u http://x > out.txt`, "file redirection"},
		{"file redirection >>", `spray -u http://x >> out.txt`, "file redirection"},
		{"stderr to file", `spray -u http://x 2>err.log`, "file redirection"},
		{"combined to file", `spray -u http://x &> all.log`, "file redirection"},
		{"chained with &&", `spray -u http://x && spray -u http://y`, "chaining"},
		{"chained with ;", `spray -u http://x ; echo done`, "chaining"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := bash.Execute(context.Background(), bashArgs(tt.cmd))
			if err == nil {
				t.Fatalf("expected error, got output %q", res.Text())
			}
			if !strings.Contains(err.Error(), tt.wantHint) {
				t.Fatalf("error = %v, want hint containing %q", err, tt.wantHint)
			}
		})
	}
}

func TestBashProxyEnvInjection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	proxy := "socks5://127.0.0.1:1080"
	bash := commands.NewBashTool(t.TempDir(), 5).WithScannerProxy(proxy)

	res, err := bash.Execute(context.Background(), bashArgs("env"))
	if err != nil {
		t.Fatalf("bash env: %v", err)
	}
	out := res.Text()
	for _, envVar := range []string{"ALL_PROXY", "all_proxy", "HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		if !strings.Contains(out, envVar+"="+proxy) {
			t.Errorf("env output missing %s", envVar)
		}
	}
}

func TestBashNoProxyEnvWhenEmpty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	bash := commands.NewBashTool(t.TempDir(), 5)

	res, err := bash.Execute(context.Background(), bashArgs("env"))
	if err != nil {
		t.Fatalf("bash env: %v", err)
	}
	if strings.Contains(res.Text(), "ALL_PROXY=socks5://") {
		t.Errorf("should not inject proxy when empty")
	}
}

// ---------------------------------------------------------------------------
// No-color injection tests (from nocolor_test.go)
// ---------------------------------------------------------------------------

func TestNormalizeNoColorInjectForScan(t *testing.T) {
	reg := commands.NewRegistry()
	cmd := &argsCapture{name: "scan"}
	reg.Register(cmd, "")

	_, err := reg.ExecuteArgs(context.Background(), []string{"scan", "-i", "10.0.0.1"})
	if err != nil {
		t.Fatalf("ExecuteArgs error: %v", err)
	}
	for _, a := range cmd.got {
		if a == "--no-color" {
			return
		}
	}
	t.Fatalf("scan should get --no-color auto-injected, got %v", cmd.got)
}

func TestNormalizeNoColorScanNoDuplicate(t *testing.T) {
	reg := commands.NewRegistry()
	cmd := &argsCapture{name: "scan"}
	reg.Register(cmd, "")

	_, err := reg.ExecuteArgs(context.Background(), []string{"scan", "-i", "10.0.0.1", "--no-color"})
	if err != nil {
		t.Fatalf("ExecuteArgs error: %v", err)
	}
	count := 0
	for _, a := range cmd.got {
		if a == "--no-color" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("--no-color should appear exactly once, got %d in %v", count, cmd.got)
	}
}

func TestNormalizeNoColorSkipsNonScan(t *testing.T) {
	reg := commands.NewRegistry()
	cmd := &argsCapture{name: "gogo"}
	reg.Register(cmd, "")

	_, err := reg.ExecuteArgs(context.Background(), []string{"gogo", "-i", "10.0.0.1"})
	if err != nil {
		t.Fatalf("ExecuteArgs error: %v", err)
	}
	for _, a := range cmd.got {
		if a == "--no-color" {
			t.Fatalf("gogo should not get --no-color, got %v", cmd.got)
		}
	}
}

// ---------------------------------------------------------------------------
// Pipe tests (from pipe_test.go)
// ---------------------------------------------------------------------------

const sampleOutput = `[critical] aws-access-key  .aws/credentials
[info] generic-api-key  src/config.js
[high] github-pat  .env.production
[critical] stripe-secret  payment/handler.go
[info] slack-webhook  deploy/notify.sh
`

func TestPseudoPipeGrep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	bash := newBashWithPseudo(t.TempDir(), &outputCommand{name: "sample", output: sampleOutput})

	res, err := bash.Execute(context.Background(), bashArgs(`sample -i . | grep critical`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := strings.TrimSpace(res.Text())
	t.Logf("output:\n%s", out)

	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	for _, line := range lines {
		if !strings.Contains(line, "critical") {
			t.Errorf("line %q should contain 'critical'", line)
		}
	}
}

func TestPseudoPipeHead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	bash := newBashWithPseudo(t.TempDir(), &outputCommand{name: "sample", output: sampleOutput})

	res, err := bash.Execute(context.Background(), bashArgs(`sample -i . | head -2`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := strings.TrimSpace(res.Text())
	t.Logf("output:\n%s", out)

	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
}

func TestPseudoPipeWc(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	bash := newBashWithPseudo(t.TempDir(), &outputCommand{name: "sample", output: sampleOutput})

	res, err := bash.Execute(context.Background(), bashArgs(`sample -i . | wc -l`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := strings.TrimSpace(res.Text())
	t.Logf("output: %q", out)

	if out != "5" {
		t.Errorf("expected 5 lines, got %q", out)
	}
}

func TestPseudoPipeChain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	bash := newBashWithPseudo(t.TempDir(), &outputCommand{name: "sample", output: sampleOutput})

	res, err := bash.Execute(context.Background(), bashArgs(`sample -i . | grep -v info | wc -l`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := strings.TrimSpace(res.Text())
	t.Logf("output: %q", out)

	if out != "3" {
		t.Errorf("expected 3 (critical+high), got %q", out)
	}
}

func TestPseudoPipeAwk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	bash := newBashWithPseudo(t.TempDir(), &outputCommand{name: "sample", output: sampleOutput})

	res, err := bash.Execute(context.Background(), bashArgs(`sample -i . | awk '{print $1}' | sort | uniq -c | sort -rn`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := strings.TrimSpace(res.Text())
	t.Logf("output:\n%s", out)

	if !strings.Contains(out, "[critical]") {
		t.Error("should contain [critical] count")
	}
	if !strings.Contains(out, "[info]") {
		t.Error("should contain [info] count")
	}
}

func TestPseudoPipeGrepRegexWithPipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	bash := newBashWithPseudo(t.TempDir(), &outputCommand{name: "sample", output: sampleOutput})

	// The regex "critical|high" is inside quotes — the | in the regex should not
	// be treated as a pipe delimiter.
	res, err := bash.Execute(context.Background(), bashArgs(`sample -i . | grep -E "critical|high"`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := strings.TrimSpace(res.Text())
	t.Logf("output:\n%s", out)

	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (2 critical + 1 high), got %d: %v", len(lines), lines)
	}
}

func TestDoublesPipeStillRejected(t *testing.T) {
	bash := newBashWithPseudo(t.TempDir(), &outputCommand{name: "sample", output: "ok"})

	_, err := bash.Execute(context.Background(), bashArgs(`sample -i . || echo fallback`))
	if err == nil {
		t.Fatal("expected error for ||, got nil")
	}
	t.Logf("correctly rejected: %v", err)
}

func TestChainStillRejected(t *testing.T) {
	bash := newBashWithPseudo(t.TempDir(), &outputCommand{name: "sample", output: "ok"})

	_, err := bash.Execute(context.Background(), bashArgs(`sample -i . && echo next`))
	if err == nil {
		t.Fatal("expected error for &&, got nil")
	}
	t.Logf("correctly rejected: %v", err)
}

func TestRedirectionStillRejected(t *testing.T) {
	bash := newBashWithPseudo(t.TempDir(), &outputCommand{name: "sample", output: "ok"})

	_, err := bash.Execute(context.Background(), bashArgs(`sample -i . > out.txt`))
	if err == nil {
		t.Fatal("expected error for >, got nil")
	}
	t.Logf("correctly rejected: %v", err)
}

func TestNoPipeStillWorks(t *testing.T) {
	bash := newBashWithPseudo(t.TempDir(), &outputCommand{name: "sample", output: "all findings here\n"})

	res, err := bash.Execute(context.Background(), bashArgs(`sample -i .`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Text(), "all findings here") {
		t.Errorf("output %q should contain expected text", res.Text())
	}
}

func TestShellPipeStillWorks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	bash := newBashWithPseudo(t.TempDir(), &outputCommand{name: "sample", output: "x"})

	res, err := bash.Execute(context.Background(), bashArgs(`echo -e "line1\nline2\nline3" | wc -l`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := strings.TrimSpace(res.Text())
	if out != "3" {
		t.Errorf("expected 3, got %q", out)
	}
}

func TestPseudoFlagWithPipeChar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	cmd := &outputCommand{name: "sample", output: "match\n"}
	bash := newBashWithPseudo(t.TempDir(), cmd)

	// -e "a|b" — the | inside quotes is part of the regex, not a pipe.
	// This should run without pipe splitting.
	res, err := bash.Execute(context.Background(), bashArgs(`sample -e "a|b"`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Text(), "match") {
		t.Errorf("output %q should contain 'match'", res.Text())
	}
}

// ---------------------------------------------------------------------------
// Panic recovery tests (from recover_test.go)
// ---------------------------------------------------------------------------

func TestExecuteTool_RecoversPanic(t *testing.T) {
	reg := commands.NewRegistry()
	reg.RegisterTool(&panicTool{msg: "boom"})

	result, err := reg.ExecuteTool(context.Background(), "panic_tool", "{}")
	if err == nil {
		t.Fatal("expected error from panicking tool, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should contain panic message, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "tool panic_tool panic") {
		t.Fatalf("error should identify the tool, got: %s", err.Error())
	}
	if result.Text() != "" {
		t.Fatalf("result should be empty on panic, got: %s", result.Text())
	}
}

func TestExecuteTool_NormalToolUnaffected(t *testing.T) {
	reg := commands.NewRegistry()
	reg.RegisterTool(&normalTool{})

	result, err := reg.ExecuteTool(context.Background(), "normal_tool", "{}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text() != "hello" {
		t.Fatalf("expected 'hello', got: %s", result.Text())
	}
}

func TestExecuteTool_PanicDoesNotAffectSubsequentCalls(t *testing.T) {
	reg := commands.NewRegistry()
	reg.RegisterTool(&panicTool{msg: "crash"})
	reg.RegisterTool(&normalTool{})

	// Call 1: panics — should be recovered and returned as error.
	_, err := reg.ExecuteTool(context.Background(), "panic_tool", "{}")
	if err == nil {
		t.Fatal("expected error from panicking tool")
	}
	t.Logf("call 1 (panic_tool): recovered panic → err=%v", err)

	// Call 2: normal tool after the panic — must succeed.
	result, err := reg.ExecuteTool(context.Background(), "normal_tool", "{}")
	if err != nil {
		t.Fatalf("normal tool failed after panic recovery: %v", err)
	}
	if result.Text() != "hello" {
		t.Fatalf("expected 'hello', got: %s", result.Text())
	}
	t.Logf("call 2 (normal_tool): succeeded after panic → result=%q", result.Text())

	// Call 3: panic again — still recoverable.
	_, err = reg.ExecuteTool(context.Background(), "panic_tool", "{}")
	if err == nil {
		t.Fatal("expected error from second panicking call")
	}
	t.Logf("call 3 (panic_tool): recovered again → err=%v", err)

	// Call 4: normal tool still works after repeated panics.
	result, err = reg.ExecuteTool(context.Background(), "normal_tool", "{}")
	if err != nil {
		t.Fatalf("normal tool failed after second panic: %v", err)
	}
	t.Logf("call 4 (normal_tool): still works → result=%q", result.Text())
}
