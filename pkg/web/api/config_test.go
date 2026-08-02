package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	configpkg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/probe"
	"github.com/chainreactors/aiscan/pkg/runner"
	types "github.com/chainreactors/aiscan/pkg/types"
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

func (f *fakeConfigStore) PrepareDistributeConfig(_ context.Context, cfg *types.DistributeConfig) (*PreparedConfig, error) {
	return &PreparedConfig{Config: cfg, TargetPath: "config.yaml"}, nil
}

func (f *fakeConfigStore) CommitDistributeConfig(_ context.Context, prepared *PreparedConfig) error {
	f.cfg = prepared.Config
	return nil
}

func (f *fakeConfigStore) DiscardDistributeConfig(*PreparedConfig) {}

func newConfig(store ConfigStore) *Config {
	return NewConfig(ConfigOptions{Store: store})
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

func testConn(ctx context.Context, store ConfigStore, section string, config *types.DistributeConfig) ([]*types.ConnectionCheck, error) {
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

func TestActivateLLMProfileSelectsByID(t *testing.T) {
	store := &fakeConfigStore{}
	store.cfg = &types.DistributeConfig{Llm: &types.LLMConfig{
		ActiveProfile: "primary",
		Providers: []*types.LLMProviderConfig{
			{Id: "primary", Name: "Primary", Provider: "openai", Model: "gpt-primary", ApiKey: "key-1"},
			{Id: "fast", Name: "Fast", Provider: "openai", Model: "deepseek-fast", ApiKey: "key-2"},
		},
	}}

	status, err := newConfig(store).Activate(context.Background(), "fast")
	if err != nil {
		t.Fatal(err)
	}
	// Selection is by id: the list order is untouched and Active() resolves
	// the chosen profile.
	if store.cfg.Llm.ActiveProfile != "fast" || store.cfg.Llm.Providers[0].Id != "primary" {
		t.Fatalf("active profile not switched by id: %+v", store.cfg.Llm)
	}
	if active := configpkg.ActiveLLMProvider(store.cfg.Llm); active.Provider != "openai" || active.Model != "deepseek-fast" || active.ApiKey != "key-2" {
		t.Fatalf("Active() did not resolve the selected profile: %+v", active)
	}
	if status.GetLlm().GetActiveProfile() != "fast" || status.GetLlm().GetActive().GetProvider() != "openai" || status.GetLlm().GetActive().GetModel() != "deepseek-fast" {
		t.Fatalf("view not synchronized: %+v", status.GetLlm())
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
	view := ConfigView(conf, "aiscan.yaml", true)
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

func TestSaveConfigRejectsNegativeModelLimits(t *testing.T) {
	store := &fakeConfigStore{}
	for _, mutate := range []func(*types.LLMProviderConfig){
		func(p *types.LLMProviderConfig) { p.MaxTokens = -1 },
		func(p *types.LLMProviderConfig) { p.ContextWindow = -1 },
		func(p *types.LLMProviderConfig) { p.Timeout = -1 },
	} {
		profile := &types.LLMProviderConfig{Id: "bad", Model: "test-model"}
		mutate(profile)
		conf := &types.DistributeConfig{Llm: &types.LLMConfig{
			Providers: []*types.LLMProviderConfig{profile},
		}}
		if _, err := newConfig(store).Save(context.Background(), conf); err == nil {
			t.Fatal("Save() accepted a negative model limit")
		}
		if store.cfg != nil && len(store.cfg.GetLlm().GetProviders()) != 0 {
			t.Fatal("invalid config was persisted")
		}
	}
}

func TestSaveConfigRejectsEmptyProfileModel(t *testing.T) {
	store := &fakeConfigStore{}
	conf := &types.DistributeConfig{Llm: &types.LLMConfig{
		Providers: []*types.LLMProviderConfig{{Id: "empty", Name: "Empty", Model: "  "}},
	}}

	if _, err := newConfig(store).Save(context.Background(), conf); err == nil {
		t.Fatal("Save() accepted an empty profile model")
	}
	if store.cfg != nil && len(store.cfg.GetLlm().GetProviders()) != 0 {
		t.Fatal("invalid config was persisted")
	}
}

func TestActivateLLMProfileRejectsEmptyModel(t *testing.T) {
	store := &fakeConfigStore{}
	store.cfg = &types.DistributeConfig{Llm: &types.LLMConfig{
		ActiveProfile: "primary",
		Providers: []*types.LLMProviderConfig{
			{Id: "primary", Model: "gpt-primary"},
			{Id: "empty", Model: ""},
		},
	}}

	if _, err := newConfig(store).Activate(context.Background(), "empty"); err == nil {
		t.Fatal("Activate() accepted an empty model")
	}
	if store.cfg.Llm.ActiveProfile != "primary" {
		t.Fatalf("active profile = %q, want primary", store.cfg.Llm.ActiveProfile)
	}
}

type transactionalConfigStore struct {
	mu         sync.Mutex
	cfg        *types.DistributeConfig
	commitErr  error
	discarded  int
	prepareLog []string
}

func (s *transactionalConfigStore) GetDistributeConfig(context.Context) (string, bool, *types.DistributeConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return "config.yaml", true, s.cfg, nil
}

func (s *transactionalConfigStore) PrepareDistributeConfig(_ context.Context, cfg *types.DistributeConfig) (*PreparedConfig, error) {
	s.mu.Lock()
	s.prepareLog = append(s.prepareLog, activeModel(cfg))
	s.mu.Unlock()
	return &PreparedConfig{Config: cfg, TargetPath: "config.yaml"}, nil
}

func (s *transactionalConfigStore) CommitDistributeConfig(_ context.Context, prepared *PreparedConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.commitErr != nil {
		return s.commitErr
	}
	s.cfg = prepared.Config
	return nil
}

func (s *transactionalConfigStore) DiscardDistributeConfig(*PreparedConfig) {
	s.mu.Lock()
	s.discarded++
	s.mu.Unlock()
}

func activeModel(c *types.DistributeConfig) string {
	if active := configpkg.ActiveLLMProvider(c.GetLlm()); active != nil {
		return active.Model
	}
	return ""
}

type recordingCloser struct {
	once sync.Once
	done chan struct{}
}

func newRecordingApp() (*runner.App, <-chan struct{}) {
	closer := &recordingCloser{done: make(chan struct{})}
	return &runner.App{Engines: closer}, closer.done
}

func (c *recordingCloser) Close() {
	c.once.Do(func() { close(c.done) })
}

func configForModel(model string) *types.DistributeConfig {
	return &types.DistributeConfig{Llm: &types.LLMConfig{
		ActiveProfile: "primary",
		Providers:     []*types.LLMProviderConfig{{Id: "primary", Provider: "openai", Model: model}},
	}}
}

func TestSaveConfigBuildFailureKeepsCommittedConfigAndSkipsApply(t *testing.T) {
	store := &transactionalConfigStore{cfg: configForModel("old-model")}
	config := NewConfig(ConfigOptions{
		Store: store,
		Build: func(_ context.Context, prepared *PreparedConfig) (*runner.App, error) {
			if got := activeModel(prepared.Config); got != "new-model" {
				t.Fatalf("candidate model = %q", got)
			}
			return nil, errors.New("candidate build failed")
		},
		Apply: func(*runner.App) { t.Fatal("apply called after build failure") },
	})

	if _, err := config.Save(context.Background(), configForModel("new-model")); err == nil {
		t.Fatal("Save() succeeded despite candidate build failure")
	}
	_, _, committed, err := store.GetDistributeConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := activeModel(committed); got != "old-model" {
		t.Fatalf("committed model = %q, want old-model", got)
	}
	if store.discarded != 1 {
		t.Fatalf("discarded candidates = %d, want 1", store.discarded)
	}
}

func TestSaveConfigCommitFailureClosesCandidate(t *testing.T) {
	store := &transactionalConfigStore{cfg: configForModel("old-model"), commitErr: errors.New("disk full")}
	candidateApp, candidateClosed := newRecordingApp()
	config := NewConfig(ConfigOptions{
		Store: store,
		Build: func(context.Context, *PreparedConfig) (*runner.App, error) {
			return candidateApp, nil
		},
		Apply: func(*runner.App) { t.Fatal("apply called after commit failure") },
	})

	if _, err := config.Save(context.Background(), configForModel("new-model")); err == nil {
		t.Fatal("Save() succeeded despite commit failure")
	}
	select {
	case <-candidateClosed:
	default:
		t.Fatal("candidate app was not closed after commit failure")
	}
}

func TestSaveConfigSerializesConcurrentCandidates(t *testing.T) {
	store := &transactionalConfigStore{cfg: configForModel("old-model")}
	entered := make(chan string, 2)
	releaseFirst := make(chan struct{})
	config := NewConfig(ConfigOptions{
		Store: store,
		Build: func(_ context.Context, prepared *PreparedConfig) (*runner.App, error) {
			model := activeModel(prepared.Config)
			entered <- model
			if model == "first-model" {
				<-releaseFirst
			}
			app, _ := newRecordingApp()
			return app, nil
		},
	})

	firstDone := make(chan error, 1)
	go func() {
		_, err := config.Save(context.Background(), configForModel("first-model"))
		firstDone <- err
	}()
	if got := <-entered; got != "first-model" {
		t.Fatalf("first candidate = %q", got)
	}

	secondDone := make(chan error, 1)
	go func() {
		_, err := config.Save(context.Background(), configForModel("second-model"))
		secondDone <- err
	}()
	select {
	case model := <-entered:
		t.Fatalf("second candidate %q entered before first commit", model)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if got := <-entered; got != "second-model" {
		t.Fatalf("second candidate = %q", got)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	_, _, committed, err := store.GetDistributeConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := activeModel(committed); got != "second-model" {
		t.Fatalf("final committed model = %q", got)
	}
}

func TestTestConnUnknownSection(t *testing.T) {
	if _, err := testConn(context.Background(), &fakeConfigStore{}, "agent", configWith(nil)); err == nil {
		t.Fatal("expected error for untestable section")
	}
}

func TestProbeCyberhubSuccess(t *testing.T) {
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

func TestProbeCyberhubAuthError(t *testing.T) {
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

func TestProbeFofaSuccessAndStoredKeyFallback(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("key")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": false, "username": "alice", "email": "a@b.c", "fofa_point": 4200,
		})
	}))
	defer srv.Close()
	orig := probe.FofaInfoEndpoint
	probe.FofaInfoEndpoint = srv.URL
	defer func() { probe.FofaInfoEndpoint = orig }()

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

func TestProbeFofaError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": true, "errmsg": "[-700] account invalid"})
	}))
	defer srv.Close()
	orig := probe.FofaInfoEndpoint
	probe.FofaInfoEndpoint = srv.URL
	defer func() { probe.FofaInfoEndpoint = orig }()

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

func TestProbeHunterSuccess(t *testing.T) {
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
	orig := probe.HunterSearchEndpoint
	probe.HunterSearchEndpoint = srv.URL
	defer func() { probe.HunterSearchEndpoint = orig }()

	resp, _ := testConn(context.Background(), &fakeConfigStore{}, "recon", configWith(func(c *types.DistributeConfig) {
		c.Recon = &types.ReconConfig{HunterApiKey: "hk"}
	}))
	if c, ok := findCheck(resp, "hunter"); !ok || !c.Ok {
		t.Fatalf("expected hunter ok, got %+v", resp)
	}
}

func TestProbeHunterError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 401, "message": "invalid api-key"})
	}))
	defer srv.Close()
	orig := probe.HunterSearchEndpoint
	probe.HunterSearchEndpoint = srv.URL
	defer func() { probe.HunterSearchEndpoint = orig }()

	resp, _ := testConn(context.Background(), &fakeConfigStore{}, "recon", configWith(func(c *types.DistributeConfig) {
		c.Recon = &types.ReconConfig{HunterToken: "bad"}
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

func TestProbeIOASuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/spaces" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "1", "name": "default", "nodes": []any{}}})
	}))
	defer srv.Close()

	resp, err := testConn(context.Background(), &fakeConfigStore{}, "ioa", configWith(func(c *types.DistributeConfig) {
		c.Ioa = &types.IOAConfig{Url: srv.URL, Token: "t"}
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c, ok := findCheck(resp, "ioa")
	if !ok || !c.Ok {
		t.Fatalf("expected ioa ok, got %+v", resp)
	}
	if !strings.Contains(c.Detail, "1 space") {
		t.Fatalf("expected space count in detail, got %q", c.Detail)
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
