package commands_test

import (
	"context"
	"io"
	"runtime"
	"strings"
	"testing"

	tmux "github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/commands"
)

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

const sampleOutput = `[critical] aws-access-key  .aws/credentials
[info] generic-api-key  src/config.js
[high] github-pat  .env.production
[critical] stripe-secret  payment/handler.go
[info] slack-webhook  deploy/notify.sh
`

// ---------- Test 1: pipe to grep ----------

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

// ---------- Test 2: pipe to head ----------

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

// ---------- Test 3: pipe to wc -l ----------

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

// ---------- Test 4: chained pipes ----------

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

// ---------- Test 5: pipe to awk ----------

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

// ---------- Test 6: pipe with grep -E regex containing | ----------

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

// ---------- Test 7: || (logical OR) still rejected ----------

func TestDoublesPipeStillRejected(t *testing.T) {
	bash := newBashWithPseudo(t.TempDir(), &outputCommand{name: "sample", output: "ok"})

	_, err := bash.Execute(context.Background(), bashArgs(`sample -i . || echo fallback`))
	if err == nil {
		t.Fatal("expected error for ||, got nil")
	}
	t.Logf("correctly rejected: %v", err)
}

// ---------- Test 8: && still rejected ----------

func TestChainStillRejected(t *testing.T) {
	bash := newBashWithPseudo(t.TempDir(), &outputCommand{name: "sample", output: "ok"})

	_, err := bash.Execute(context.Background(), bashArgs(`sample -i . && echo next`))
	if err == nil {
		t.Fatal("expected error for &&, got nil")
	}
	t.Logf("correctly rejected: %v", err)
}

// ---------- Test 9: > redirection still rejected ----------

func TestRedirectionStillRejected(t *testing.T) {
	bash := newBashWithPseudo(t.TempDir(), &outputCommand{name: "sample", output: "ok"})

	_, err := bash.Execute(context.Background(), bashArgs(`sample -i . > out.txt`))
	if err == nil {
		t.Fatal("expected error for >, got nil")
	}
	t.Logf("correctly rejected: %v", err)
}

// ---------- Test 10: no pipe — existing behavior preserved ----------

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

// ---------- Test 11: shell-only commands still support pipes ----------

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

// ---------- Test 12: pseudo-command with flag containing pipe char ----------

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

// bashArgs is already defined in bash_scanner_test.go
