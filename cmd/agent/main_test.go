package main

import (
	"context"
	"encoding/json"
	"github.com/chainreactors/cyber/core/telemetry"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentCLIExposesOnlyLocalAgentOptions(t *testing.T) {
	parsed, option, err := parseOptions([]string{"--model", "fixture", "--prompt", "inspect", "--workdir", t.TempDir()}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.WorkDir == "" || option.Model != "fixture" || option.Prompt != "inspect" || option.Transport != "local" {
		t.Fatalf("parsed options = %+v, %+v", parsed, option)
	}
	if _, _, err := parseOptions([]string{"--cyberhub-url", "https://example.test"}, io.Discard); err == nil {
		t.Fatal("minimal agent accepted scanner flags")
	}
}

func TestAgentProfileConstructionIsInert(t *testing.T) {
	profile, err := newAgentProfile(cfg.Option{}, telemetry.NopLogger(), t.TempDir(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if profile.extensions.Active() {
		t.Fatal("profile became active during construction")
	}
}

func TestAgentDependencyClosureExcludesFullApplicationFeatures(t *testing.T) {
	output, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"/exts/scanner", "/exts/search", "/exts/proxy", "/exts/ioa",
		"/exts/browser", "/exts/record", "/exts/web", "/exts/okf", "/pkg/web",
		"/tools/proxy", "/tools/record", "/tools/okf",
	}
	for _, dependency := range strings.Fields(string(output)) {
		for _, fragment := range forbidden {
			if strings.Contains(dependency, fragment) {
				t.Errorf("minimal agent links forbidden dependency %s", dependency)
			}
		}
	}
}

func TestAgentOneShotRunsAgainstOpenAICompatibleGateway(t *testing.T) {
	called := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var request struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if request.Model != "fixture" {
			t.Errorf("model = %q, want fixture", request.Model)
		}
		select {
		case called <- request.Model:
		default:
		}
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"fixture response\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"fixture response"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	for _, format := range []string{"text", "json", "jsonl", "markdown", "csv"} {
		for _, fileOutput := range []bool{false, true} {
			name := format + "/stdout"
			if fileOutput {
				name = format + "/file"
			}
			t.Run(name, func(t *testing.T) {
				var stdout, stderr strings.Builder
				dir := t.TempDir()
				args := []string{"--provider", "openai", "--base-url", server.URL, "--api-key", "test", "--model", "fixture",
					"--prompt", "hello", "--workdir", dir, "--timeout", "10", "--quiet", "--no-color", "-o", format}
				path := filepath.Join(dir, "result.out")
				if fileOutput {
					args = append(args, "-f", path)
				}
				err := run(context.Background(), args, &stdout, &stderr)
				if err != nil {
					t.Fatalf("agent run: %v\nstderr: %s", err, stderr.String())
				}
				body := stdout.String()
				if fileOutput {
					if body != "" {
						t.Fatalf("file output leaked to stdout: %q", body)
					}
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					body = string(data)
				}
				if format == "csv" {
					if !strings.HasPrefix(body, "target,tool,kind,") {
						t.Fatalf("CSV output=%q", body)
					}
				} else if !strings.Contains(body, "fixture response") {
					t.Fatalf("missing task response in %q", body)
				}
				if format == "json" && !json.Valid([]byte(body)) {
					t.Fatalf("invalid JSON: %q", body)
				}
				if format == "jsonl" {
					for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
						if !json.Valid([]byte(line)) {
							t.Fatalf("invalid JSONL: %q", line)
						}
					}
				}
			})
		}
	}
}

func TestAgentHelpAndStartupErrorOutput(t *testing.T) {
	var output strings.Builder
	if _, _, err := parseOptions([]string{"--help"}, &output); err == nil || !strings.Contains(output.String(), "prompt") {
		t.Fatalf("help error=%v output=%q", err, output.String())
	}
	for _, args := range [][]string{
		{"--json", "--provider", "invalid-fixture"},
		{"--json", "--timeout", "-1"},
		{"--json", "--prompt", "fixture", "--task-file", "missing"},
		{"--json", "--unknown-fixture"},
		{"--json", "unexpected-positional"},
	} {
		output.Reset()
		err := run(t.Context(), args, &output, io.Discard)
		var result struct {
			IsError bool `json:"is_error"`
		}
		if decodeErr := json.Unmarshal([]byte(output.String()), &result); decodeErr != nil {
			t.Fatalf("%v: err=%v decode=%v", args, err, decodeErr)
		}
		if err == nil || !result.IsError {
			t.Fatalf("args=%v err=%v output=%s", args, err, output.String())
		}
	}
}
