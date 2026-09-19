package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
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
		"/pkg/exts/scanner", "/pkg/exts/search", "/pkg/exts/proxy", "/pkg/exts/ioa",
		"/pkg/exts/browser", "/pkg/exts/record", "/pkg/exts/web", "/pkg/exts/okf", "/pkg/web",
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
			Model string `json:"model"`
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
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"fixture response"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	var stdout, stderr strings.Builder
	err := run(context.Background(), []string{
		"--provider", "openai", "--base-url", server.URL, "--api-key", "test", "--model", "fixture",
		"--prompt", "hello", "--workdir", t.TempDir(), "--timeout", "10", "--quiet", "--no-color",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("agent run failed: %v\nstderr: %s", err, stderr.String())
	}
	select {
	case model := <-called:
		if model != "fixture" {
			t.Fatalf("gateway model = %q, want fixture", model)
		}
	default:
		t.Fatalf("agent did not call the configured gateway; stdout = %q", stdout.String())
	}
}
