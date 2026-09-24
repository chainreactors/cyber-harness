package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	crtm "github.com/chainreactors/crtm/pkg"
	"github.com/chainreactors/cyber/audit/internal/toolchain"
)

// TestSingleFileRelease exercises an already-built release, not the test binary
// or fake provisioning. The only copied distribution file is the executable.
func TestSingleFileRelease(t *testing.T) {
	input := os.Getenv("AUDIT_SINGLEFILE_BINARY")
	if input == "" {
		t.Skip("set AUDIT_SINGLEFILE_BINARY to a bundled release")
	}
	root := t.TempDir()
	executable := filepath.Join(root, crtm.BinaryName("cyber-audit"))
	body, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, body, 0755); err != nil {
		t.Fatal(err)
	}
	var downloads atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		http.Error(w, "external network disabled", http.StatusBadGateway)
	}))
	defer proxy.Close()
	var env []string
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		switch strings.ToUpper(key) {
		case "PATH", "HOME", "USERPROFILE", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		env = append(env, value)
	}
	// No installed audit CLI or Go/npm toolchain can satisfy preflight via PATH.
	path := filepath.Join(root, "empty-path")
	if runtime.GOOS == "windows" {
		path = filepath.Join(os.Getenv("SystemRoot"), "System32")
	}
	env = append(env, "PATH="+path, "HOME="+root, "USERPROFILE="+root, "HTTP_PROXY="+proxy.URL, "HTTPS_PROXY="+proxy.URL, "ALL_PROXY="+proxy.URL, "NO_PROXY=127.0.0.1,localhost")
	run := func(want int, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable, args...)
		cmd.Dir, cmd.Env = root, env
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				t.Fatalf("%v: %v\n%s", args, err, out)
			}
		}
		if code != want {
			t.Fatalf("%v: exit %d, want %d\n%s", args, code, want, out)
		}
		return string(out)
	}
	data := filepath.Join(root, "tools-data")
	run(1, "doctor", "--data-dir", data)
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("doctor wrote data: %v", err)
	}
	out := run(0, "tools", "install", "--data-dir", data)
	for _, spec := range toolchain.Required {
		if !strings.Contains(out, spec.Name+" "+spec.Version) {
			t.Fatalf("missing tool: %s", out)
		}
	}
	manifest := filepath.Join(data, "arsenal", "manifest.json")
	before, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	run(0, "tools", "install", "--data-dir", data)
	after, err := os.ReadFile(manifest)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("repeat installation changed state: %v", err)
	}
	run(0, "doctor", "--data-dir", data)
	if err := os.Remove(filepath.Join(data, "arsenal", "bin", crtm.BinaryName("ast-grep"))); err != nil {
		t.Fatal(err)
	}
	run(0, "tools", "install", "--data-dir", data)
	run(0, "doctor", "--data-dir", data)

	workspace := filepath.Join(root, "repository")
	if err := os.Mkdir(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "fixture.ts"), []byte("console.log('audit_probe');\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c := coverage{Reviewed: true, Scope: "single-file fixture", Examined: []string{"fixture.ts"}, Excluded: []string{}, Unsupported: []string{}, Unresolved: []string{}, Checks: []checkRecord{{Tool: "osv-scanner", Status: "not_applicable", Evidence: "No dependency manifest in fixture"}, {Tool: "proton", Status: "completed", Evidence: "raw/proton.jsonl"}}}
	coverageJSON, _ := json.Marshal(c)
	steps := []struct {
		name string
		args any
	}{
		{"bash", map[string]any{"command": "arsenal remove rg"}},
		{"bash", map[string]any{"command": "arsenal install rg"}},
		{"bash", map[string]any{"command": "rg --json audit_probe fixture.ts > report/raw/rg.jsonl"}},
		{"bash", map[string]any{"command": "ast-grep run --lang ts --pattern console.log --json fixture.ts > report/raw/ast.json"}},
		{"bash", map[string]any{"command": "osv-scanner scan source --help > report/raw/osv-help.txt"}},
		{"bash", map[string]any{"command": "proton -i fixture.ts -c keys -j --no-stats -o report/raw/proton.jsonl"}},
		{"write", map[string]any{"path": "report/coverage.json", "content": string(coverageJSON)}},
		{"write", map[string]any{"path": "report/index.md", "content": "---\nokf_version: \"0.2\"\n---\n\n# Single-file audit\n\nSee [coverage](coverage.json), [findings](findings.json) and [log](log.md).\n"}},
		{"bash", map[string]any{"command": "okf validate report"}},
	}
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"ping"`) && !strings.Contains(string(body), "cyber-audit") {
			textReply(w, "pong")
			return
		}
		step := int(requests.Add(1)) - 1
		if step < len(steps) {
			toolReply(w, steps[step].name, steps[step].args)
		} else {
			streamReply(w, "Single-file release validation completed.")
		}
	}))
	defer provider.Close()
	// A second empty data directory exercises automatic preparation before model startup.
	report := filepath.Join(workspace, "report")
	run(0, "--workdir", workspace, "--data-dir", filepath.Join(root, "agent-data"), "--report-dir", report, "--provider", "openai", "--base-url", provider.URL, "--api-key", "fixture", "--model", "fixture", "-p", "Validate the fixture tools and save a report", "--quiet", "--no-color", "--timeout", "60")
	if requests.Load() != int32(len(steps)+1) {
		t.Fatalf("provider requests: %d", requests.Load())
	}
	for file, want := range map[string]string{"raw/rg.jsonl": `"type":"match"`, "raw/ast.json": "console.log", "raw/osv-help.txt": "--lockfile"} {
		body, err := os.ReadFile(filepath.Join(report, file))
		if err != nil || !strings.Contains(string(body), want) {
			t.Fatalf("%s: %v\n%s", file, err, body)
		}
	}
	if _, err := os.Stat(filepath.Join(report, "raw/proton.jsonl")); err != nil {
		t.Fatal(err)
	}
	var result runReport
	body, err = os.ReadFile(filepath.Join(report, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" {
		t.Fatalf("report %s: %s", result.Status, result.Error)
	}
	if downloads.Load() != 0 {
		t.Fatalf("attempted %d external network requests", downloads.Load())
	}
	t.Logf("single copied binary passed provisioning, recovery, CLI execution and report completion; external requests: %d", downloads.Load())
}
