package web

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/chainreactors/aiscan/pkg/probe"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	types "github.com/chainreactors/aiscan/pkg/types"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type cfgT = *types.DistributeConfig

// configWith builds a DistributeConfig, letting each test set only the fields
// it cares about. Pass nil for an empty config.
func configWith(fn func(*types.DistributeConfig)) cfgT {
	c := &types.DistributeConfig{}
	if fn != nil {
		fn(c)
	}
	return c
}

func newService(store ConfigStore) *Service {
	return NewService(ServiceConfig{ConfigStore: store})
}

func findCheck(checks []*types.ConnectionCheck, name string) (*types.ConnectionCheck, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return nil, false
}

func TestTestConnUnknownSection(t *testing.T) {
	svc := newService(&fakeConfigStore{})
	if _, err := svc.TestConn(context.Background(), "agent", configWith(nil)); err == nil {
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

	svc := newService(&fakeConfigStore{})
	cfg := configWith(func(c *types.DistributeConfig) {
		c.Cyberhub = &types.CyberhubConfig{Url: srv.URL, Key: "hub-key"}
	})
	resp, err := svc.TestConn(context.Background(), "cyberhub", cfg)
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

	svc := newService(&fakeConfigStore{})
	cfg := configWith(func(c *types.DistributeConfig) {
		c.Cyberhub = &types.CyberhubConfig{Url: srv.URL, Key: "nope"}
	})
	resp, err := svc.TestConn(context.Background(), "cyberhub", cfg)
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
	svc := newService(store)

	resp, err := svc.TestConn(context.Background(), "recon", configWith(nil))
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

	svc := newService(&fakeConfigStore{})
	resp, _ := svc.TestConn(context.Background(), "recon", configWith(func(c *types.DistributeConfig) {
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

	svc := newService(&fakeConfigStore{})
	resp, _ := svc.TestConn(context.Background(), "recon", configWith(func(c *types.DistributeConfig) {
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

	svc := newService(&fakeConfigStore{})
	resp, _ := svc.TestConn(context.Background(), "recon", configWith(func(c *types.DistributeConfig) {
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
	svc := newService(&fakeConfigStore{})
	resp, _ := svc.TestConn(context.Background(), "recon", configWith(nil))
	if c, ok := findCheck(resp, "recon"); !ok || c.Ok || c.Error == "" {
		t.Fatalf("expected a single failing recon check, got %+v", resp)
	}
}

func TestHandlerTestConnRouting(t *testing.T) {
	svc := newService(&fakeConfigStore{})
	srv := httptest.NewServer(NewHandler(svc, nil, nil, ""))
	defer srv.Close()
	client := rpc.NewConfigServiceClient(srv.Client(), srv.URL)

	response, err := client.TestConnection(context.Background(), connect.NewRequest(&types.TestConnectionRequest{
		Section: "cyberhub", Config: &types.DistributeConfig{},
	}))
	if err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if len(response.Msg.Checks) != 1 || response.Msg.Checks[0].Name != "cyberhub" {
		t.Fatalf("expected one cyberhub check, got %+v", response.Msg.Checks)
	}

	_, err = client.TestConnection(context.Background(), connect.NewRequest(&types.TestConnectionRequest{
		Section: "agent", Config: &types.DistributeConfig{},
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument for untestable section, got %v", err)
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

	svc := newService(&fakeConfigStore{})
	resp, err := svc.TestConn(context.Background(), "ioa", configWith(func(c *types.DistributeConfig) {
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

// fakeConfigStore is a minimal in-memory ConfigStore for probe tests.
type fakeConfigStore struct {
	cfg *types.DistributeConfig
}

func (f *fakeConfigStore) current() *types.DistributeConfig {
	if f.cfg == nil {
		f.cfg = &types.DistributeConfig{}
	}
	return f.cfg
}

func (f *fakeConfigStore) GetDistributeConfig(ctx context.Context) (string, bool, *types.DistributeConfig, error) {
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
	res, err := svc.TestLLM(context.Background(), &types.LLMProbeRequest{
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
	svc := NewService(ServiceConfig{ConfigStore: &fakeConfigStore{}})
	res, err := svc.TestLLM(context.Background(), &types.LLMProbeRequest{Provider: "openai", ApiKey: "sk-test"})
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
	svc := NewService(ServiceConfig{ConfigStore: store})

	// APIKey left blank: the stored secret must be used.
	res, err := svc.TestLLM(context.Background(), &types.LLMProbeRequest{
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
	svc := NewService(ServiceConfig{ConfigStore: &fakeConfigStore{}})
	// Unroutable port → connection refused, surfaced inside the result.
	res, err := svc.TestLLM(context.Background(), &types.LLMProbeRequest{
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

	svc := NewService(ServiceConfig{ConfigStore: &fakeConfigStore{}})
	res, err := svc.ListLLMModels(context.Background(), &types.LLMProbeRequest{
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
	svc := NewService(ServiceConfig{ConfigStore: store})

	// APIKey left blank: the stored secret must be used.
	res, err := svc.ListLLMModels(context.Background(), &types.LLMProbeRequest{
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
	svc := NewService(ServiceConfig{ConfigStore: store})

	res, err := svc.ListLLMModels(context.Background(), &types.LLMProbeRequest{
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

	svc := NewService(ServiceConfig{ConfigStore: &fakeConfigStore{}})
	res, err := svc.ListLLMModels(context.Background(), &types.LLMProbeRequest{
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
	svc := NewService(ServiceConfig{ConfigStore: &fakeConfigStore{}})
	res, err := svc.ListLLMModels(context.Background(), &types.LLMProbeRequest{
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

	svc := NewService(ServiceConfig{ConfigStore: &fakeConfigStore{}})
	res, err := svc.ListLLMModels(context.Background(), &types.LLMProbeRequest{
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
