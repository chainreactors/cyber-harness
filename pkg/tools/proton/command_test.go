package proton_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tmux "github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/pkg/commands"
	protoncmd "github.com/chainreactors/aiscan/pkg/tools/proton"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// e2eBash creates a BashTool with the proton pseudo-command registered,
// wired through the full tmux.Manager pipe infrastructure.
func e2eBash(t *testing.T) (*commands.BashTool, string) {
	t.Helper()
	dir := t.TempDir()

	registry := commands.NewRegistry()
	rs := &resources.Set{}
	cmd := protoncmd.New().WithResourceProvider(rs.ProtonConfig)
	cmd.SetWorkDir(dir)
	registry.Register(cmd, "proton")

	bash := commands.NewBashTool(dir, 30)
	bash.Manager().SetCommands(func(name string) (tmux.Command, bool) {
		return registry.Get(name)
	})
	bash.Manager().SetExecHooks(
		func(w io.Writer) { commands.Output.Reset(w) },
		func() { commands.Output.Reset(nil) },
	)
	bash.Manager().SetWorkDir(dir)
	return bash, dir
}

func run(t *testing.T, bash *commands.BashTool, cmd string) string {
	t.Helper()
	data, _ := json.Marshal(map[string]string{"command": cmd})
	res, err := bash.Execute(context.Background(), string(data))
	if err != nil {
		t.Fatalf("execute %q: %v", cmd, err)
	}
	return res.Text()
}


func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0755)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func requireUnix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
}

// ---------------------------------------------------------------------------
// E2E: proton -i <file>  (direct file scan)
// ---------------------------------------------------------------------------

func TestE2E_ProtonScanFile_DetectsAllCategories(t *testing.T) {
	bash, dir := e2eBash(t)
	path := writeFile(t, dir, "all_secrets.txt", `
# Cloud
AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE
AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY

# Tokens
GITHUB_TOKEN=ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij
GITLAB_TOKEN=glpat-ABCDEFGHIJKLMNOPqrst
SLACK_TOKEN=xoxb-1234567890-abcdefghij

# Payment
STRIPE_KEY=sk_test_FAKE00000000000000000000ab

# Private key
-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEA...
-----END RSA PRIVATE KEY-----

# JWT
eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U

# Database
DATABASE_URL=postgres://admin:s3cret@db.example.com:5432/myapp

# Generic
API_KEY="sk_prod_abcdefghijklmnopqrst"
SECRET_KEY="mysupersecretkey12345"
password="hunter2isnotgood"

# Credentials in URL
WEBHOOK=https://admin:pa55word@hooks.example.com/notify

# Internal
BACKEND=10.0.1.50:8080
`)
	out := run(t, bash, "proton -i "+path)
	t.Logf("output:\n%s", out)

	// Verify key patterns were detected (match by content, not template ID)
	expects := []string{
		"AKIAIOSFODNN7EXAMPLE",
		"ghp_",
		"glpat-",
		"private-key",
		"findings",
	}
	for _, s := range expects {
		if !strings.Contains(out, s) {
			t.Errorf("output should contain %q", s)
		}
	}
}

// ---------------------------------------------------------------------------
// E2E: proton -i <dir>  (directory walk)
// ---------------------------------------------------------------------------

func TestE2E_ProtonScanDir_MultiFile(t *testing.T) {
	bash, dir := e2eBash(t)

	writeFile(t, dir, "src/config.py", `
API_KEY = "sk_test_FAKE00000000000000000000dc"
DB = "mysql://root:toor@10.0.0.1:3306/app"
`)
	writeFile(t, dir, "src/utils.go", `
package utils
// no secrets here
func Hello() string { return "world" }
`)
	writeFile(t, dir, "deploy/.env", `
GITHUB_TOKEN=ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij
SECRET_KEY="production_secret_key_value"
`)
	// Should be skipped (binary extension)
	writeFile(t, dir, "assets/logo.png", "not really a png but has the extension")

	out := run(t, bash, "proton -i "+dir)
	t.Logf("output:\n%s", out)

	if !strings.Contains(out, "config.py") {
		t.Error("should scan config.py")
	}
	if !strings.Contains(out, ".env") {
		t.Error("should scan .env")
	}
	if strings.Contains(out, "utils.go") {
		t.Error("should not report findings in clean utils.go")
	}
}

// ---------------------------------------------------------------------------
// E2E: proton -e <regex>  (custom expression)
// ---------------------------------------------------------------------------

func TestE2E_ProtonExpression(t *testing.T) {
	bash, dir := e2eBash(t)
	writeFile(t, dir, "data.txt", `
normal line
CUSTOM_MATCH_12345
another line
CUSTOM_MATCH_67890
`)

	out := run(t, bash, `proton -i `+dir+` -e "CUSTOM_MATCH_[0-9]+"`)
	t.Logf("output:\n%s", out)

	if !strings.Contains(out, "CUSTOM_MATCH_12345") {
		t.Error("should match first custom pattern")
	}
	if !strings.Contains(out, "CUSTOM_MATCH_67890") {
		t.Error("should match second custom pattern")
	}
}

// ---------------------------------------------------------------------------
// E2E: proton --severity  (filter)
// ---------------------------------------------------------------------------

func TestE2E_ProtonSeverityFilter(t *testing.T) {
	bash, dir := e2eBash(t)
	writeFile(t, dir, "mixed.txt", `
AKIAIOSFODNN7EXAMPLE
ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij
sk_test_FAKE00000000000000000000ab
`)

	out := run(t, bash, "proton -i "+dir+" --severity high")
	t.Logf("output:\n%s", out)

	if !strings.Contains(out, "high") {
		t.Error("should contain high severity findings")
	}
	if strings.Contains(out, "[low]") {
		t.Error("should NOT contain low severity")
	}
	if strings.Contains(out, "[info]") {
		t.Error("should NOT contain info severity")
	}
}

// ---------------------------------------------------------------------------
// E2E: proton -j  (JSON output)
// ---------------------------------------------------------------------------

func TestE2E_ProtonJSON(t *testing.T) {
	bash, dir := e2eBash(t)
	writeFile(t, dir, "secret.txt", `TOKEN=ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij`)

	out := run(t, bash, "proton -i "+dir+" -j")
	t.Logf("output:\n%s", out)

	jsonLines := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" || strings.HasPrefix(line, "[proton]") {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("not valid JSON: %q", line)
			continue
		}
		jsonLines++
		if _, ok := m["template-id"]; !ok {
			t.Errorf("JSON missing template-id: %v", m)
		}
		if _, ok := m["severity"]; !ok {
			t.Errorf("JSON missing severity: %v", m)
		}
	}
	if jsonLines == 0 {
		t.Error("expected at least one JSON finding")
	}
}

// ---------------------------------------------------------------------------
// E2E: echo ... | proton  (shell → pseudo pipe, stdin)
// ---------------------------------------------------------------------------

func TestE2E_Pipe_EchoToProton(t *testing.T) {
	requireUnix(t)
	bash, _ := e2eBash(t)

	out := run(t, bash, `echo -e "AKIAIOSFODNN7EXAMPLE\nghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij\nnormal_text" | proton`)
	t.Logf("output:\n%s", out)

	if !strings.Contains(out, "AKIA") {
		t.Error("should detect AWS key pattern from pipe")
	}
	if !strings.Contains(out, "findings") {
		t.Error("should show summary")
	}
}

// ---------------------------------------------------------------------------
// E2E: cat file | proton  (shell → pseudo pipe)
// ---------------------------------------------------------------------------

func TestE2E_Pipe_CatToProton(t *testing.T) {
	requireUnix(t)
	bash, dir := e2eBash(t)
	writeFile(t, dir, "leaked.conf", `
[database]
url = postgres://dbuser:dbpass123@10.0.2.1:5432/prod
[github]
token = ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij
`)

	out := run(t, bash, "cat "+filepath.Join(dir, "leaked.conf")+" | proton")
	t.Logf("output:\n%s", out)

	if !strings.Contains(out, "ghp_") {
		t.Error("should detect GitHub token from pipe")
	}
	if !strings.Contains(out, "findings") {
		t.Error("should show findings summary")
	}
}

// ---------------------------------------------------------------------------
// E2E: cat file | proton -e <regex>  (shell → pseudo pipe + expression)
// ---------------------------------------------------------------------------

func TestE2E_Pipe_CatToProtonWithExpression(t *testing.T) {
	requireUnix(t)
	bash, dir := e2eBash(t)
	writeFile(t, dir, "log.txt", `
2024-01-15 auth: user=admin action=login ip=192.168.1.100
2024-01-15 auth: user=root action=sudo ip=10.0.0.1
2024-01-15 app: user=guest action=view ip=172.16.0.5
`)

	out := run(t, bash, `cat `+filepath.Join(dir, "log.txt")+` | proton -e "user=root"`)
	t.Logf("output:\n%s", out)

	if !strings.Contains(out, "user=root") {
		t.Error("should match custom expression from pipe")
	}
}

// ---------------------------------------------------------------------------
// E2E: proton -i file | grep critical  (pseudo → shell pipe)
// ---------------------------------------------------------------------------

func TestE2E_Pipe_ProtonToGrep(t *testing.T) {
	requireUnix(t)
	bash, dir := e2eBash(t)
	writeFile(t, dir, "mix.txt", `
AKIAIOSFODNN7EXAMPLE
ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij
sk_test_FAKE00000000000000000000ab
`)

	out := run(t, bash, "proton -i "+dir+" | grep high")
	t.Logf("output:\n%s", out)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		if !strings.Contains(line, "high") {
			t.Errorf("grep should only pass high lines, got: %q", line)
		}
	}
	if len(lines) == 0 {
		t.Error("expected at least one high severity finding")
	}
}

// ---------------------------------------------------------------------------
// E2E: proton -i file | wc -l  (pseudo → shell pipe, count)
// ---------------------------------------------------------------------------

func TestE2E_Pipe_ProtonToWc(t *testing.T) {
	requireUnix(t)
	bash, dir := e2eBash(t)
	writeFile(t, dir, "keys.txt", `
AKIAIOSFODNN7EXAMPLE
ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij
sk_test_FAKE00000000000000000000ab
`)

	out := run(t, bash, "proton -i "+dir+" -j | wc -l")
	t.Logf("output: %q", strings.TrimSpace(out))

	count := strings.TrimSpace(out)
	// Should have at least 3 JSON lines (one per distinct finding)
	if count == "0" || count == "" {
		t.Error("wc -l should return non-zero count")
	}
}

// ---------------------------------------------------------------------------
// E2E: proton -i file | grep | wc  (pseudo → chained shell pipes)
// ---------------------------------------------------------------------------

func TestE2E_Pipe_ProtonToGrepToWc(t *testing.T) {
	requireUnix(t)
	bash, dir := e2eBash(t)
	writeFile(t, dir, "all.txt", `
AKIAIOSFODNN7EXAMPLE
ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij
sk_test_FAKE00000000000000000000ab
api_key="medium_severity_key_here_123"
10.0.1.5:8080
`)

	out := run(t, bash, "proton -i "+dir+" | grep high | wc -l")
	t.Logf("output: %q", strings.TrimSpace(out))

	count := strings.TrimSpace(out)
	if count == "0" {
		t.Error("should have high severity findings")
	}
}

// ---------------------------------------------------------------------------
// E2E: curl simulation | proton  (simulated HTTP response)
// ---------------------------------------------------------------------------

func TestE2E_Pipe_CurlSimToProton(t *testing.T) {
	requireUnix(t)
	bash, dir := e2eBash(t)

	// Simulate a curl response by writing a realistic API response
	writeFile(t, dir, "api_response.json", `{
  "config": {
    "aws_access_key": "AKIAIOSFODNN7EXAMPLE",
    "aws_secret_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
    "database_url": "postgres://admin:p@ss@db.internal:5432/prod",
    "debug": true
  }
}`)

	// cat simulates what curl would return
	out := run(t, bash, "cat "+filepath.Join(dir, "api_response.json")+" | proton")
	t.Logf("output:\n%s", out)

	if !strings.Contains(out, "AKIA") {
		t.Error("should detect AWS key in API response")
	}
	if !strings.Contains(out, "findings") {
		t.Error("should show findings summary")
	}
}

// ---------------------------------------------------------------------------
// E2E: echo | proton -j | grep  (shell → pseudo → shell, full chain)
// ---------------------------------------------------------------------------

func TestE2E_Pipe_ShellToPseudoToShell(t *testing.T) {
	requireUnix(t)
	bash, _ := e2eBash(t)

	// This tests the full three-stage pipeline isn't supported in one command yet,
	// but pseudo | shell is supported. Test the two-step approach:
	// Step 1: echo | proton -j  (shell → pseudo)
	out := run(t, bash, `echo "AKIAIOSFODNN7EXAMPLE" | proton -j`)
	t.Logf("step1 output:\n%s", out)

	found := false
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" || strings.HasPrefix(line, "[proton]") {
			continue
		}
		var m map[string]interface{}
		if json.Unmarshal([]byte(line), &m) == nil {
			if id, ok := m["template-id"].(string); ok && strings.Contains(id, "aws") {
				found = true
			}
		}
	}
	if !found {
		t.Error("should detect AWS key in echo → proton -j pipe")
	}
}

// ---------------------------------------------------------------------------
// E2E: proton (no args) → error
// ---------------------------------------------------------------------------

func TestE2E_ProtonNoArgs_Error(t *testing.T) {
	bash, _ := e2eBash(t)
	out := run(t, bash, "proton")
	t.Logf("output: %s", out)
	if !strings.Contains(out, "target required") && !strings.Contains(out, "exit code") {
		t.Error("proton with no args should report error")
	}
}

// ---------------------------------------------------------------------------
// E2E: proton --help
// ---------------------------------------------------------------------------

func TestE2E_ProtonHelp(t *testing.T) {
	bash, _ := e2eBash(t)
	out := run(t, bash, "proton --help")
	if !strings.Contains(out, "proton -") {
		t.Error("--help should print usage")
	}
}

// ---------------------------------------------------------------------------
// E2E: no findings → clean output
// ---------------------------------------------------------------------------

func TestE2E_ProtonNoFindings(t *testing.T) {
	bash, dir := e2eBash(t)
	writeFile(t, dir, "clean.txt", `
Hello World
This file has no secrets
Just regular text content
x = 42
`)

	out := run(t, bash, "proton -i "+filepath.Join(dir, "clean.txt"))
	t.Logf("output:\n%s", out)

	if !strings.Contains(out, "no findings") {
		t.Error("should report 'no findings' for clean file")
	}
}
