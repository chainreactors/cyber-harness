package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

func TestLogLLMProbeStatusReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "probe-1",
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "pong"}, "finish_reason": "stop"},
			},
		})
	}))
	defer srv.Close()

	var logBuf bytes.Buffer
	logger := telemetry.NewLogger(telemetry.LogConfig{Debug: true, Output: &logBuf})

	logLLMProbeStatus(context.Background(), agent.ProviderConfig{
		Provider: "openai",
		BaseURL:  srv.URL + "/v1",
		APIKey:   "sk-test",
		Model:    "gpt-test",
	}, logger)

	logText := logBuf.String()
	if !strings.Contains(logText, "● llm") ||
		!strings.Contains(logText, "openai/gpt-test") ||
		!strings.Contains(logText, "ms") {
		t.Fatalf("missing ready probe log:\n%s", logText)
	}
}

func TestLogLLMProbeStatusUnready(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	var logBuf bytes.Buffer
	logger := telemetry.NewLogger(telemetry.LogConfig{Output: &logBuf})

	logLLMProbeStatus(context.Background(), agent.ProviderConfig{
		Provider: "openai",
		BaseURL:  srv.URL + "/v1",
		APIKey:   "sk-test",
		Model:    "gpt-test",
	}, logger)

	logText := logBuf.String()
	if !strings.Contains(logText, "● fail llm") ||
		!strings.Contains(logText, "openai/gpt-test") ||
		!strings.Contains(logText, "ms") ||
		!strings.Contains(logText, "unauthorized") {
		t.Fatalf("missing unready probe log:\n%s", logText)
	}
}

func TestAppLoggerCanBeRetargeted(t *testing.T) {
	var first, second bytes.Buffer
	app := &App{}
	app.SetLogger(telemetry.NewLogger(telemetry.LogConfig{Debug: true, Output: &first}))
	logger := app.Logger()

	logger.Infof("before")
	app.SetLogger(telemetry.NewLogger(telemetry.LogConfig{Debug: true, Output: &second}))
	logger.Infof("after")

	if !strings.Contains(first.String(), "before") {
		t.Fatalf("initial logger missing: %q", first.String())
	}
	if strings.Contains(first.String(), "after") {
		t.Fatalf("retargeted log went to old writer: %q", first.String())
	}
	if !strings.Contains(second.String(), "after") {
		t.Fatalf("retargeted logger missing: %q", second.String())
	}
}
