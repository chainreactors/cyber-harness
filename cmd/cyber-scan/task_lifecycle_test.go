package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/telemetry"
	taskcli "github.com/chainreactors/cyber/pkg/cli/task"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/profile"
)

type closeFailureProfile struct {
	profile.Profile
	err    error
	calls  int
	output *strings.Builder
	t      *testing.T
}

func (p *closeFailureProfile) Close(ctx context.Context) error {
	p.calls++
	if p.output.Len() != 0 {
		p.t.Error("JSON result emitted before cleanup")
	}
	if ctx.Err() != nil {
		p.t.Error("cleanup inherited canceled context")
	}
	return errors.Join(p.Profile.Close(ctx), p.err)
}

func TestOneShotIncludesProfileCloseFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"fixture"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	option := &cfg.Option{LLMOptions: cfg.LLMOptions{Provider: "openai", BaseURL: server.URL, APIKey: "fixture", Model: "fixture"}, AgentOptions: cfg.AgentOptions{Prompt: "functional output test"}, MiscOptions: cfg.MiscOptions{OutputFormat: "json", DataDir: t.TempDir(), Quiet: true}}
	var stdout strings.Builder
	output := taskcli.NewOutput(option, &stdout, io.Discard)
	closeErr := errors.New("fixture recorder flush failed")
	var active *closeFailureProfile
	build := func(request profile.Request) (profile.Profile, error) {
		p, err := newCyberScanProfile(request)
		if err != nil {
			return nil, err
		}
		active = &closeFailureProfile{Profile: p, err: closeErr, output: &stdout, t: t}
		return active, nil
	}
	err := output.Finish(runOneShotMode(t.Context(), build, option, telemetry.NopLogger(), output))
	if !errors.Is(err, closeErr) || active.calls != 1 {
		t.Fatalf("err=%v close calls=%d", err, active.calls)
	}
	var result struct {
		IsError bool `json:"is_error"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout.String()), &result); err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Error.Message, closeErr.Error()) {
		t.Fatalf("result=%s", stdout.String())
	}
}

func TestCLIStartupErrorsAreStructured(t *testing.T) {
	t.Chdir(t.TempDir())
	configPath := filepath.Join(t.TempDir(), "cyber.yaml")
	if err := os.WriteFile(configPath, []byte("llm:\n  provider: openai\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, failure := range [][]string{
		{"--timeout", "-1"},
		{"--task-file", filepath.Join(t.TempDir(), "missing")},
		{"--provider", "invalid-fixture"},
		{"--unknown-fixture"},
	} {
		var stdout strings.Builder
		args := append([]string{"--json", "--config", configPath, "agent", "--prompt", "fixture"}, failure...)
		err := runCLI(t.Context(), args, strings.NewReader(""), &stdout, io.Discard, nil)
		var result struct {
			IsError bool `json:"is_error"`
		}
		if decodeErr := json.Unmarshal([]byte(stdout.String()), &result); decodeErr != nil {
			t.Fatalf("%v: %v: %v", failure, err, decodeErr)
		}
		if err == nil || !result.IsError {
			t.Fatalf("%v: err=%v result=%s", failure, err, stdout.String())
		}
	}
}

type brokenOutput struct{ err error }

func (w brokenOutput) Write([]byte) (int, error) { return 0, w.err }

func TestScannerHelpIsIndependentOfModelConfiguration(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("CYBER_PROVIDER", "invalid-fixture")
	path := filepath.Join(t.TempDir(), "cyber.yaml")
	if err := os.WriteFile(path, []byte("llm:\n  active_profile: missing\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	err := runCLI(t.Context(), []string{"--config", path, "proton", "--help"}, strings.NewReader(""), &out, io.Discard, nil)
	if err != nil || out.Len() == 0 {
		t.Fatalf("err=%v help=%q", err, out.String())
	}
	writeErr := errors.New("help sink failed")
	err = runCLI(t.Context(), []string{"--config", path, "proton", "--help"}, strings.NewReader(""), brokenOutput{writeErr}, io.Discard, nil)
	if !errors.Is(err, writeErr) {
		t.Fatalf("writer error lost: %v", err)
	}
}

func TestCyberScannerParseFailurePreservesOutput(t *testing.T) {
	for _, args := range [][]string{
		{"--ai", "--json", "--unknown-fixture", "proton"},
	} {
		var out strings.Builder
		err := runCLI(t.Context(), args, strings.NewReader(""), &out, io.Discard, nil)
		var result struct {
			IsError bool `json:"is_error"`
		}
		if decodeErr := json.Unmarshal([]byte(out.String()), &result); decodeErr != nil || !result.IsError || err == nil {
			t.Fatalf("args=%v err=%v decode=%v output=%q", args, err, decodeErr, out.String())
		}
	}
}
