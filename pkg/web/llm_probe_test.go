package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/agent/probe"
	proto "github.com/chainreactors/aiscan/core/config"
)

// fakeConfigStore is a minimal in-memory ConfigStore for probe tests.
type fakeConfigStore struct {
	cfg proto.DistributeConfig
}

func (f *fakeConfigStore) GetDistributeConfig(ctx context.Context) (string, bool, proto.DistributeConfig, error) {
	return "config.yaml", true, f.cfg, nil
}

func (f *fakeConfigStore) PrepareDistributeConfig(_ context.Context, cfg proto.DistributeConfig) (*PreparedConfig, error) {
	return &PreparedConfig{Config: cfg, TargetPath: "config.yaml"}, nil
}

func (f *fakeConfigStore) CommitDistributeConfig(_ context.Context, prepared *PreparedConfig) error {
	f.cfg = prepared.Config
	return nil
}

func (f *fakeConfigStore) DiscardDistributeConfig(*PreparedConfig) {}

// stubLLMServer emulates an OpenAI-compatible /chat/completions endpoint and
// records the Authorization header it received.
func stubLLMServer(t *testing.T, reply string, gotAuth *string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if gotAuth != nil {
			*gotAuth = r.Header.Get("Authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "cmpl-1",
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": reply}, "finish_reason": "stop"},
			},
		})
	})
	return httptest.NewServer(mux)
}

func TestTestLLMSuccess(t *testing.T) {
	srv := stubLLMServer(t, "pong", nil)
	defer srv.Close()

	svc := NewService(ServiceConfig{ConfigStore: &fakeConfigStore{}})
	res, err := svc.TestLLM(context.Background(), probe.LLMProbeRequest{
		Provider: "openai",
		BaseURL:  srv.URL + "/v1",
		APIKey:   "sk-test",
		Model:    "gpt-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected ok, got error: %q", res.Error)
	}
	if res.Reply != "pong" {
		t.Fatalf("expected reply pong, got %q", res.Reply)
	}
	if res.LatencyMs < 0 {
		t.Fatalf("expected non-negative latency, got %d", res.LatencyMs)
	}
}

func TestTestLLMMissingModel(t *testing.T) {
	svc := NewService(ServiceConfig{ConfigStore: &fakeConfigStore{}})
	res, err := svc.TestLLM(context.Background(), probe.LLMProbeRequest{Provider: "openai", APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OK {
		t.Fatal("expected failure when model is empty")
	}
	if !strings.Contains(res.Error, "model") {
		t.Fatalf("expected model error, got %q", res.Error)
	}
}

func TestTestLLMFallsBackToStoredKey(t *testing.T) {
	var gotAuth string
	srv := stubLLMServer(t, "ok", &gotAuth)
	defer srv.Close()

	store := &fakeConfigStore{}
	store.cfg.LLM.Providers = []proto.LLMProviderConfig{{ID: "default", Provider: "openai", APIKey: "sk-stored"}}
	svc := NewService(ServiceConfig{ConfigStore: store})

	// APIKey left blank: the stored secret must be used.
	res, err := svc.TestLLM(context.Background(), probe.LLMProbeRequest{
		Provider: "openai",
		BaseURL:  srv.URL + "/v1",
		Model:    "gpt-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected ok, got error: %q", res.Error)
	}
	if gotAuth != "Bearer sk-stored" {
		t.Fatalf("expected stored key in Authorization header, got %q", gotAuth)
	}
}

func TestTestLLMReportsTransportError(t *testing.T) {
	svc := NewService(ServiceConfig{ConfigStore: &fakeConfigStore{}})
	// Unroutable port → connection refused, surfaced inside the result.
	res, err := svc.TestLLM(context.Background(), probe.LLMProbeRequest{
		Provider: "openai",
		BaseURL:  "http://127.0.0.1:1/v1",
		APIKey:   "sk-test",
		Model:    "gpt-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OK {
		t.Fatal("expected failure against unreachable endpoint")
	}
	if res.Error == "" {
		t.Fatal("expected an error message")
	}
}

// stubModelsServer emulates an OpenAI-compatible GET /models endpoint returning
// the given IDs, recording the Authorization header it received.
func stubModelsServer(t *testing.T, ids []string, gotAuth *string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if gotAuth != nil {
			*gotAuth = r.Header.Get("Authorization")
		}
		data := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]any{"id": id, "object": "model"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	})
	return httptest.NewServer(mux)
}

func TestListLLMModelsSuccess(t *testing.T) {
	var gotAuth string
	srv := stubModelsServer(t, []string{"gpt-4.1", "deepseek-v4-pro"}, &gotAuth)
	defer srv.Close()

	svc := NewService(ServiceConfig{ConfigStore: &fakeConfigStore{}})
	res, err := svc.ListLLMModels(context.Background(), probe.LLMProbeRequest{
		Provider: "openai",
		BaseURL:  srv.URL + "/v1",
		APIKey:   "sk-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected ok, got error: %q", res.Error)
	}
	if len(res.Models) != 2 || res.Models[0] != "gpt-4.1" {
		t.Fatalf("unexpected models: %v", res.Models)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("expected bearer key in Authorization header, got %q", gotAuth)
	}
}

func TestListLLMModelsFallsBackToStoredKey(t *testing.T) {
	var gotAuth string
	srv := stubModelsServer(t, []string{"m1"}, &gotAuth)
	defer srv.Close()

	store := &fakeConfigStore{}
	store.cfg.LLM.Providers = []proto.LLMProviderConfig{{ID: "default", Provider: "openai", APIKey: "sk-stored"}}
	svc := NewService(ServiceConfig{ConfigStore: store})

	// APIKey left blank: the stored secret must be used.
	res, err := svc.ListLLMModels(context.Background(), probe.LLMProbeRequest{
		Provider: "openai",
		BaseURL:  srv.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected ok, got error: %q", res.Error)
	}
	if gotAuth != "Bearer sk-stored" {
		t.Fatalf("expected stored key in Authorization header, got %q", gotAuth)
	}
}

func TestListLLMModelsUsesSelectedProfileStoredKey(t *testing.T) {
	var gotAuth string
	srv := stubModelsServer(t, []string{"m1"}, &gotAuth)
	defer srv.Close()

	store := &fakeConfigStore{}
	store.cfg.LLM.ActiveProfile = "primary"
	store.cfg.LLM.Providers = []proto.LLMProviderConfig{
		{ID: "primary", Provider: "openai", APIKey: "sk-primary"},
		{ID: "secondary", Provider: "openai", APIKey: "sk-secondary"},
	}
	svc := NewService(ServiceConfig{ConfigStore: store})

	res, err := svc.ListLLMModels(context.Background(), probe.LLMProbeRequest{
		ProfileID: "secondary",
		Provider:  "openai",
		BaseURL:   srv.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected ok, got error: %q", res.Error)
	}
	if gotAuth != "Bearer sk-secondary" {
		t.Fatalf("expected selected profile key, got %q", gotAuth)
	}
}

func TestListLLMModelsTreatsNotFoundAsUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	svc := NewService(ServiceConfig{ConfigStore: &fakeConfigStore{}})
	res, err := svc.ListLLMModels(context.Background(), probe.LLMProbeRequest{
		Provider: "openai",
		BaseURL:  srv.URL + "/v1",
		APIKey:   "sk-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK || res.Supported || res.Error != "" {
		t.Fatalf("result = %+v, want graceful unsupported response", res)
	}
}

func TestListLLMModelsReportsTransportError(t *testing.T) {
	svc := NewService(ServiceConfig{ConfigStore: &fakeConfigStore{}})
	res, err := svc.ListLLMModels(context.Background(), probe.LLMProbeRequest{
		Provider: "openai",
		BaseURL:  "http://127.0.0.1:1/v1",
		APIKey:   "sk-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OK {
		t.Fatal("expected failure against unreachable endpoint")
	}
	if res.Error == "" {
		t.Fatal("expected an error message")
	}
}

// TestListLLMModelsAnthropic guards the fix for the Anthropic provider: it must
// enumerate models via GET {base}/models (with x-api-key + anthropic-version)
// rather than short-circuiting on the modelLister assertion with "provider does
// not support listing models".
func TestListLLMModelsAnthropic(t *testing.T) {
	var gotKey, gotVersion string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "claude-opus-4-8", "object": "model"},
				{"id": "glm-5.2", "object": "model"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := NewService(ServiceConfig{ConfigStore: &fakeConfigStore{}})
	res, err := svc.ListLLMModels(context.Background(), probe.LLMProbeRequest{
		Provider: "anthropic",
		BaseURL:  srv.URL + "/v1",
		APIKey:   "sk-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected ok, got error: %q", res.Error)
	}
	if len(res.Models) != 2 || res.Models[0] != "claude-opus-4-8" {
		t.Fatalf("unexpected models: %v", res.Models)
	}
	if gotKey != "sk-test" {
		t.Fatalf("expected x-api-key header, got %q", gotKey)
	}
	if gotVersion == "" {
		t.Fatal("expected anthropic-version header to be set")
	}
}
