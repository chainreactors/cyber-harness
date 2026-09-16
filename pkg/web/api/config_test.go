package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	configpkg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/resource"
	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
	searchext "github.com/chainreactors/cyber/pkg/exts/search"
	types "github.com/chainreactors/cyber/pkg/types"
)

// fakeConfigStore is a minimal in-memory ConfigStore.
type fakeConfigStore struct {
	cfg *types.DistributeConfig
}

func (f *fakeConfigStore) current() *types.DistributeConfig {
	if f.cfg == nil {
		f.cfg = &types.DistributeConfig{}
	}
	return f.cfg
}

func (f *fakeConfigStore) GetDistributeConfig(context.Context) (string, bool, *types.DistributeConfig, error) {
	return "config.yaml", true, f.current(), nil
}

func (f *fakeConfigStore) SaveConfig(context.Context, *types.DistributeConfig) (*types.ConfigView, error) {
	return nil, errors.New("configuration updates unavailable in connection fixture")
}

func (f *fakeConfigStore) ActivateConfig(context.Context, string) (*types.ConfigView, error) {
	return nil, errors.New("configuration activation unavailable in connection fixture")
}

func newConfig(backend ConfigBackend) *Config {
	sections := configpkg.NewSections()
	resources := resource.New()
	if _, err := resource.Define[configpkg.Connection](resources, sections.ConnectionPoint()); err != nil {
		panic(err)
	}
	if err := scannerext.Declare(resources); err != nil {
		panic(err)
	}
	if err := searchext.Declare(resources); err != nil {
		panic(err)
	}
	resources.Freeze()
	return NewConfig(backend, ConfigOptions{Sections: sections})
}

// configWith builds a DistributeConfig, letting each test set only the fields
// it cares about. Pass nil for an empty config.
func configWith(fn func(*types.DistributeConfig)) *types.DistributeConfig {
	c := &types.DistributeConfig{}
	if fn != nil {
		fn(c)
	}
	return c
}

func findCheck(checks []*types.ConnectionCheck, name string) (*types.ConnectionCheck, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return nil, false
}

func testConn(ctx context.Context, store ConfigBackend, section string, config *types.DistributeConfig) ([]*types.ConnectionCheck, error) {
	resp, err := newConfig(store).TestConnection(ctx, &types.TestConnectionRequest{Section: section, Config: config})
	if err != nil {
		return nil, err
	}
	return resp.Checks, nil
}

func TestValidateLLMConfigRejectsUnsupportedProvider(t *testing.T) {
	cfg := &types.LLMConfig{Providers: []*types.LLMProviderConfig{{
		Provider: "bogus-vendor",
		Model:    "some-model",
	}}}
	if err := ValidateLLMConfig(cfg); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("ValidateLLMConfig() error = %v", err)
	}
}

func TestValidateLLMConfigAcceptsVendorAlias(t *testing.T) {
	cfg := &types.LLMConfig{Providers: []*types.LLMProviderConfig{{
		Provider: "deepseek",
		Model:    "deepseek-chat",
	}}}
	if err := ValidateLLMConfig(cfg); err != nil {
		t.Fatalf("ValidateLLMConfig() error = %v", err)
	}
}

func TestConfigStatusIncludesModelLimits(t *testing.T) {
	images := false
	conf := &types.DistributeConfig{Llm: &types.LLMConfig{
		ActiveProfile: "large",
		Providers: []*types.LLMProviderConfig{{
			Id: "large", Provider: "anthropic", Model: "glm-5.2[1m]",
			MaxTokens: 32768, ContextWindow: 1000000, Timeout: 45, Images: &images,
		}},
	}}
	view := ConfigView(conf, "cyber.yaml", true)
	if view.GetLlm().GetActive().GetMaxTokens() != 32768 || view.GetLlm().GetActive().GetContextWindow() != 1000000 {
		t.Fatalf("active limits missing from view: %+v", view.GetLlm())
	}
	if len(view.GetLlm().GetProviders()) != 1 || view.GetLlm().GetProviders()[0].GetMaxTokens() != 32768 || view.GetLlm().GetProviders()[0].GetContextWindow() != 1000000 {
		t.Fatalf("profile limits missing from view: %+v", view.GetLlm().GetProviders())
	}
	active := view.GetLlm().GetActive()
	if active.GetTimeout() != 45 || active.Images == nil || active.GetImages() {
		t.Fatalf("provider capabilities missing from view: %+v", active)
	}
}

func TestTestConnUnknownSection(t *testing.T) {
	if _, err := testConn(context.Background(), &fakeConfigStore{}, "agent", configWith(nil)); err == nil {
		t.Fatal("expected error for untestable section")
	}
}

func TestConnectionCyberhubSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/fingerprints/export") {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-API-Key") != "hub-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "message": "ok",
			"data": map[string]any{"fingerprints": []any{map[string]any{"name": "tomcat"}}, "total": 1},
		})
	}))
	defer srv.Close()

	cfg := configWith(func(c *types.DistributeConfig) {
		c.Cyberhub = &types.CyberhubConfig{Url: srv.URL, Key: "hub-key"}
	})
	resp, err := testConn(context.Background(), &fakeConfigStore{}, "cyberhub", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c, ok := findCheck(resp, "cyberhub"); !ok || !c.Ok {
		t.Fatalf("expected cyberhub ok, got %+v", resp)
	}
}

func TestConnectionCyberhubAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("bad key"))
	}))
	defer srv.Close()

	cfg := configWith(func(c *types.DistributeConfig) {
		c.Cyberhub = &types.CyberhubConfig{Url: srv.URL, Key: "nope"}
	})
	resp, err := testConn(context.Background(), &fakeConfigStore{}, "cyberhub", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c, _ := findCheck(resp, "cyberhub"); c.Ok {
		t.Fatal("expected cyberhub failure, got ok")
	}
}

func TestConnectionFofaSuccessAndStoredKeyFallback(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("key")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": false, "username": "alice", "email": "a@b.c", "fofa_point": 4200,
		})
	}))
	defer srv.Close()
	orig := scannerext.FofaInfoEndpoint
	scannerext.FofaInfoEndpoint = srv.URL
	defer func() { scannerext.FofaInfoEndpoint = orig }()

	// FOFA key left blank in the request: the stored secret must be used.
	store := &fakeConfigStore{}
	store.cfg = &types.DistributeConfig{Recon: &types.ReconConfig{FofaKey: "stored-fofa"}}

	resp, err := testConn(context.Background(), store, "recon", configWith(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c, ok := findCheck(resp, "fofa")
	if !ok || !c.Ok {
		t.Fatalf("expected fofa ok, got %+v", resp)
	}
	if gotKey != "stored-fofa" {
		t.Fatalf("expected stored key, server saw %q", gotKey)
	}
	if !strings.Contains(c.Detail, "alice") {
		t.Fatalf("expected username in detail, got %q", c.Detail)
	}
}

func TestConnectionFofaError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": true, "errmsg": "[-700] account invalid"})
	}))
	defer srv.Close()
	orig := scannerext.FofaInfoEndpoint
	scannerext.FofaInfoEndpoint = srv.URL
	defer func() { scannerext.FofaInfoEndpoint = orig }()

	resp, _ := testConn(context.Background(), &fakeConfigStore{}, "recon", configWith(func(c *types.DistributeConfig) {
		c.Recon = &types.ReconConfig{FofaKey: "bad"}
	}))
	c, ok := findCheck(resp, "fofa")
	if !ok || c.Ok {
		t.Fatalf("expected fofa failure, got %+v", resp)
	}
	if !strings.Contains(c.Error, "account invalid") {
		t.Fatalf("expected errmsg surfaced, got %q", c.Error)
	}
}

func TestConnectionHunterSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api-key") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200, "message": "success", "data": map[string]any{"total": 7},
		})
	}))
	defer srv.Close()
	orig := scannerext.HunterSearchEndpoint
	scannerext.HunterSearchEndpoint = srv.URL
	defer func() { scannerext.HunterSearchEndpoint = orig }()

	resp, _ := testConn(context.Background(), &fakeConfigStore{}, "recon", configWith(func(c *types.DistributeConfig) {
		c.Recon = &types.ReconConfig{HunterApiKey: "hk"}
	}))
	if c, ok := findCheck(resp, "hunter"); !ok || !c.Ok {
		t.Fatalf("expected hunter ok, got %+v", resp)
	}
}

func TestConnectionHunterError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 401, "message": "invalid api-key"})
	}))
	defer srv.Close()
	orig := scannerext.HunterSearchEndpoint
	scannerext.HunterSearchEndpoint = srv.URL
	defer func() { scannerext.HunterSearchEndpoint = orig }()

	resp, _ := testConn(context.Background(), &fakeConfigStore{}, "recon", configWith(func(c *types.DistributeConfig) {
		c.Recon = &types.ReconConfig{HunterApiKey: "bad"}
	}))
	c, ok := findCheck(resp, "hunter")
	if !ok || c.Ok {
		t.Fatalf("expected hunter failure, got %+v", resp)
	}
	if !strings.Contains(c.Error, "invalid api-key") {
		t.Fatalf("expected hunter message surfaced, got %q", c.Error)
	}
}

func TestReconNoCredentials(t *testing.T) {
	resp, _ := testConn(context.Background(), &fakeConfigStore{}, "recon", configWith(nil))
	if c, ok := findCheck(resp, "recon"); !ok || c.Ok || c.Error == "" {
		t.Fatalf("expected a single failing recon check, got %+v", resp)
	}
}

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

	res, err := newConfig(&fakeConfigStore{}).TestLLM(context.Background(), &types.LLMProbeRequest{
		Provider: "openai",
		BaseUrl:  srv.URL + "/v1",
		ApiKey:   "sk-test",
		Model:    "gpt-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Ok {
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
	res, err := newConfig(&fakeConfigStore{}).TestLLM(context.Background(), &types.LLMProbeRequest{Provider: "openai", ApiKey: "sk-test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Ok {
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
	store.cfg = &types.DistributeConfig{Llm: &types.LLMConfig{
		Providers: []*types.LLMProviderConfig{{Id: "default", Provider: "openai", ApiKey: "sk-stored"}},
	}}

	// APIKey left blank: the stored secret must be used.
	res, err := newConfig(store).TestLLM(context.Background(), &types.LLMProbeRequest{
		Provider: "openai",
		BaseUrl:  srv.URL + "/v1",
		Model:    "gpt-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Ok {
		t.Fatalf("expected ok, got error: %q", res.Error)
	}
	if gotAuth != "Bearer sk-stored" {
		t.Fatalf("expected stored key in Authorization header, got %q", gotAuth)
	}
}

func TestTestLLMReportsTransportError(t *testing.T) {
	// Unroutable port → connection refused, surfaced inside the result.
	res, err := newConfig(&fakeConfigStore{}).TestLLM(context.Background(), &types.LLMProbeRequest{
		Provider: "openai",
		BaseUrl:  "http://127.0.0.1:1/v1",
		ApiKey:   "sk-test",
		Model:    "gpt-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Ok {
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

	res, err := newConfig(&fakeConfigStore{}).ListModels(context.Background(), &types.LLMProbeRequest{
		Provider: "openai",
		BaseUrl:  srv.URL + "/v1",
		ApiKey:   "sk-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Ok {
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
	store.cfg = &types.DistributeConfig{Llm: &types.LLMConfig{
		Providers: []*types.LLMProviderConfig{{Id: "default", Provider: "openai", ApiKey: "sk-stored"}},
	}}

	// APIKey left blank: the stored secret must be used.
	res, err := newConfig(store).ListModels(context.Background(), &types.LLMProbeRequest{
		Provider: "openai",
		BaseUrl:  srv.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Ok {
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
	store.cfg = &types.DistributeConfig{Llm: &types.LLMConfig{
		ActiveProfile: "primary",
		Providers: []*types.LLMProviderConfig{
			{Id: "primary", Provider: "openai", ApiKey: "sk-primary"},
			{Id: "secondary", Provider: "openai", ApiKey: "sk-secondary"},
		},
	}}

	res, err := newConfig(store).ListModels(context.Background(), &types.LLMProbeRequest{
		ProfileId: "secondary",
		Provider:  "openai",
		BaseUrl:   srv.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Ok {
		t.Fatalf("expected ok, got error: %q", res.Error)
	}
	if gotAuth != "Bearer sk-secondary" {
		t.Fatalf("expected selected profile key, got %q", gotAuth)
	}
}

func TestListLLMModelsTreatsNotFoundAsUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	res, err := newConfig(&fakeConfigStore{}).ListModels(context.Background(), &types.LLMProbeRequest{
		Provider: "openai",
		BaseUrl:  srv.URL + "/v1",
		ApiKey:   "sk-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Ok || res.Supported || res.Error != "" {
		t.Fatalf("result = %+v, want graceful unsupported response", res)
	}
}

func TestListLLMModelsReportsTransportError(t *testing.T) {
	res, err := newConfig(&fakeConfigStore{}).ListModels(context.Background(), &types.LLMProbeRequest{
		Provider: "openai",
		BaseUrl:  "http://127.0.0.1:1/v1",
		ApiKey:   "sk-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Ok {
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

	res, err := newConfig(&fakeConfigStore{}).ListModels(context.Background(), &types.LLMProbeRequest{
		Provider: "anthropic",
		BaseUrl:  srv.URL + "/v1",
		ApiKey:   "sk-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Ok {
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
