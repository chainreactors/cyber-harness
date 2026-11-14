package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func TestJEVAccelerationAndRiskComposeIndependently(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	for _, tc := range []struct {
		name, mode, risk, key string
		command               bool
	}{
		{"off", "off", "none", "", false},
		{"credential without policy", "off", "", "fixture-key", false},
		{"explicit risk only", "off", "jev", "fixture-key", false},
		{"auto only", "auto", "none", "fixture-key", true},
		{"risk and auto", "auto", "jev", "fixture-key", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := minimalConfig(nil)
			c.Option.Extensions = cfg.Values{"jev": {"mode": tc.mode, "api_key": tc.key, "directory": t.TempDir()}, "guardrail": {"provider": tc.risk}}
			p, err := buildCyberScanProfile(c)
			if err != nil {
				t.Fatal(err)
			}
			if err = p.Load(t.Context()); err != nil {
				_ = p.Close(context.Background())
				t.Fatal(err)
			}
			defer p.Close(context.Background())
			if p.commands.Has("jev") != tc.command {
				t.Fatal("mode did not control acceleration installation")
			}
			if p.guardrail == nil {
				t.Fatal("accelerator removed independent guardrail")
			}
		})
	}
}

// Exercises the actual cyber-scan extension graph and its default streaming
// session. A cold one-task smoke test proves installation, not acceleration.
func TestLiveJEVProfileHTTP(t *testing.T) {
	if os.Getenv("JEV_PROFILE_LIVE") != "1" {
		t.Skip("set JEV_PROFILE_LIVE=1 and both provider credentials for a paid profile smoke test")
	}
	for _, key := range []string{"CYBER_API_KEY", "CYBER_BASE_URL", "CYBER_MODEL", "TYPESAFE_API_KEY"} {
		if os.Getenv(key) == "" {
			t.Fatalf("missing %s", key)
		}
	}
	var reads atomic.Int64
	marker := fmt.Sprintf("local-evidence-%d", time.Now().UnixNano())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/evidence" || r.Method != "GET" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		reads.Add(1)
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintln(w, marker+" check_pass=false")
	}))
	defer server.Close()
	c := minimalConfig(&agentsession.Config{})
	c.Base.DataDir = t.TempDir()
	c.Base.Provider = provider.StartupConfig{Mode: provider.StartupRequired, Config: provider.ProviderConfig{
		Provider: os.Getenv("CYBER_PROVIDER"), BaseURL: os.Getenv("CYBER_BASE_URL"), APIKey: os.Getenv("CYBER_API_KEY"), Model: os.Getenv("CYBER_MODEL"), MaxTokens: 4096, Timeout: 90,
	}}
	c.Option.Extensions = cfg.Values{"jev": {"mode": "auto", "directory": t.TempDir()}, "guardrail": {"provider": "none"}}
	p, err := buildCyberScanProfile(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err = p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	runtime, err := p.Runtime()
	if err != nil || !p.commands.Has("jev") || !hooks.BeforeModel.Has(runtime.Hooks()) {
		t.Fatalf("accelerator not installed in cyber-scan profile: %v", err)
	}
	session, err := runtime.EnsureSession(agentsession.SessionOptions{ID: "jev-live-profile"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	started := time.Now()
	run, err := session.Run(ctx, agentsession.RunInput{Message: agent.TextInput("Retrieve the HTTP status and response body from this authorized local evidence endpoint: " + server.URL + "/evidence . Report its exact evidence marker. Say UNCONFIRMED if check_pass is false; do not infer a vulnerability from an HTTP status. Do not probe other paths."), MaxTurns: 12})
	if err != nil {
		t.Fatal(err)
	}
	result, err := run.Wait()
	correct := err == nil && result != nil && reads.Load() > 0 && strings.Contains(result.Output, marker) && strings.Contains(result.Output, "UNCONFIRMED")
	row := map[string]any{"real_l2": true, "profile": "cyber-scan", "streaming": true, "mode": "auto", "seeded_rule": false, "scan_engines_loaded": false, "correct": correct, "endpoint_reads": reads.Load(), "elapsed_ms": time.Since(started).Milliseconds()}
	if result != nil {
		row["output"], row["l2_usage"], row["turns"] = result.Output, result.TotalUsage, result.Turns
	}
	if err != nil {
		row["error"] = err.Error()
	}
	if path := os.Getenv("JEV_PROFILE_REPORT"); path != "" {
		data, marshalErr := json.MarshalIndent(row, "", "  ")
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("cyber-scan streaming profile: correct=%t reads=%d elapsed=%v", correct, reads.Load(), time.Since(started))
	if !correct {
		t.Fatalf("profile task failed: %v", err)
	}
}
