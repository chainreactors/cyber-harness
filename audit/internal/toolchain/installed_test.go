package toolchain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func installed(t *testing.T) map[string]string {
	t.Helper()
	data := os.Getenv("AUDIT_TEST_DATA_DIR")
	if data == "" {
		t.Skip("set AUDIT_TEST_DATA_DIR after tools install")
	}
	manager, err := New(data)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, s := range manager.Check(t.Context()) {
		if s.Error != "" {
			t.Fatalf("%s: %s", s.Name, s.Error)
		}
		paths[s.Name] = s.Path
	}
	return paths
}
func TestInstalledTextAndAST(t *testing.T) {
	tools := installed(t)
	dir := t.TempDir()
	for name, body := range map[string]string{"main.go": "package main\nimport \"os/exec\"\nfunc run(cmd string) { exec.Command(cmd, \"arg\") }\n", "main.ts": "function run(input: string) { console.log(input); }\n", "unknown.xyz": "input = opaque_operation()\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), tools["rg"], "--json", "input", dir)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "unknown.xyz") || !strings.Contains(string(out), "main.ts") {
		t.Fatal("multi-language text evidence missing")
	}
	for _, spec := range []struct{ language, pattern, file string }{{"go", "exec.Command($CMD, $$$ARGS)", "main.go"}, {"ts", "console.log($$$ARGS)", "main.ts"}} {
		out, err := exec.CommandContext(t.Context(), tools["ast-grep"], "run", "--lang", spec.language, "--pattern", spec.pattern, "--json", filepath.Join(dir, spec.file)).Output()
		if err != nil {
			t.Fatal(err)
		}
		var matches []any
		if err := json.Unmarshal(out, &matches); err != nil || len(matches) != 1 {
			t.Fatalf("%s AST: %s %v", spec.language, out, err)
		}
	}
}
func TestInstalledOSVFailureAndEmpty(t *testing.T) {
	tools := installed(t)
	dir := t.TempDir()
	args := []string{"scan", "source", "--no-call-analysis=go", "--no-call-analysis=rust", "--format", "json", dir}
	cmd := exec.CommandContext(t.Context(), tools["osv-scanner"], args...)
	out, err := cmd.CombinedOutput()
	var code int
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	}
	if code != 128 {
		t.Fatalf("empty unsupported input must exit 128, got %d: %.1000s", code, out)
	}
	file := filepath.Join(dir, "requirements.txt")
	os.WriteFile(file, []byte("requests==2.19.1\n"), 0600)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	cmd = exec.CommandContext(ctx, tools["osv-scanner"], args...)
	cmd.Env = append(os.Environ(), "HTTPS_PROXY=http://127.0.0.1:1", "HTTP_PROXY=http://127.0.0.1:1", "NO_PROXY=")
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("network failure reported clean: %.1000s", out)
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		t.Fatalf("network failure reported as vulnerability result: %.1000s", out)
	}
}
func TestInstalledOSVNetworkResults(t *testing.T) {
	if os.Getenv("AUDIT_NETWORK_TESTS") != "1" {
		t.Skip("set AUDIT_NETWORK_TESTS=1 for live OSV queries")
	}
	tools := installed(t)
	for _, test := range []struct {
		dependency string
		code       int
	}{{"requests==2.19.1", 1}, {"idna==3.15", 0}} {
		dir := t.TempDir()
		file := filepath.Join(dir, "requirements.txt")
		os.WriteFile(file, []byte(test.dependency+"\n"), 0600)
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		out, err := exec.CommandContext(ctx, tools["osv-scanner"], "scan", "source", "--lockfile", file, "--format", "json", "--no-call-analysis=go", "--no-call-analysis=rust").Output()
		cancel()
		code := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != test.code {
			t.Fatalf("%s: expected %d, got %d; live advisory data or network may have changed", test.dependency, test.code, code)
		}
		if !json.Valid(out) {
			t.Fatalf("invalid OSV JSON: %.500s", out)
		}
	}
}
