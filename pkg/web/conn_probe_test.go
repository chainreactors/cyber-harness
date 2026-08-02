package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/chainreactors/aiscan/pkg/probe"
	"github.com/chainreactors/aiscan/pkg/rpc/config/configconnect"
	configpb "github.com/chainreactors/aiscan/pkg/types/config"
)

type cfgT = *configpb.DistributeConfig

// configWith builds a DistributeConfig, letting each test set only the fields
// it cares about. Pass nil for an empty config.
func configWith(fn func(*configpb.DistributeConfig)) cfgT {
	c := &configpb.DistributeConfig{}
	if fn != nil {
		fn(c)
	}
	return c
}

func newService(store ConfigStore) *Service {
	return NewService(ServiceConfig{ConfigStore: store})
}

func findCheck(checks []probe.ConnCheck, name string) (probe.ConnCheck, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return probe.ConnCheck{}, false
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
	cfg := configWith(func(c *configpb.DistributeConfig) {
		c.Cyberhub = &configpb.CyberhubConfig{Url: srv.URL, Key: "hub-key"}
	})
	resp, err := svc.TestConn(context.Background(), "cyberhub", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c, ok := findCheck(resp, "cyberhub"); !ok || !c.OK {
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
	cfg := configWith(func(c *configpb.DistributeConfig) {
		c.Cyberhub = &configpb.CyberhubConfig{Url: srv.URL, Key: "nope"}
	})
	resp, err := svc.TestConn(context.Background(), "cyberhub", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c, _ := findCheck(resp, "cyberhub"); c.OK {
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
	store.cfg = &configpb.DistributeConfig{Recon: &configpb.ReconConfig{FofaKey: "stored-fofa"}}
	svc := newService(store)

	resp, err := svc.TestConn(context.Background(), "recon", configWith(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c, ok := findCheck(resp, "fofa")
	if !ok || !c.OK {
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
	resp, _ := svc.TestConn(context.Background(), "recon", configWith(func(c *configpb.DistributeConfig) {
		c.Recon = &configpb.ReconConfig{FofaKey: "bad"}
	}))
	c, ok := findCheck(resp, "fofa")
	if !ok || c.OK {
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
	resp, _ := svc.TestConn(context.Background(), "recon", configWith(func(c *configpb.DistributeConfig) {
		c.Recon = &configpb.ReconConfig{HunterApiKey: "hk"}
	}))
	if c, ok := findCheck(resp, "hunter"); !ok || !c.OK {
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
	resp, _ := svc.TestConn(context.Background(), "recon", configWith(func(c *configpb.DistributeConfig) {
		c.Recon = &configpb.ReconConfig{HunterToken: "bad"}
	}))
	c, ok := findCheck(resp, "hunter")
	if !ok || c.OK {
		t.Fatalf("expected hunter failure, got %+v", resp)
	}
	if !strings.Contains(c.Error, "invalid api-key") {
		t.Fatalf("expected hunter message surfaced, got %q", c.Error)
	}
}

func TestReconNoCredentials(t *testing.T) {
	svc := newService(&fakeConfigStore{})
	resp, _ := svc.TestConn(context.Background(), "recon", configWith(nil))
	if c, ok := findCheck(resp, "recon"); !ok || c.OK || c.Error == "" {
		t.Fatalf("expected a single failing recon check, got %+v", resp)
	}
}

func TestHandlerTestConnRouting(t *testing.T) {
	svc := newService(&fakeConfigStore{})
	srv := httptest.NewServer(NewHandler(svc, nil, nil, nil, nil, ""))
	defer srv.Close()
	client := configconnect.NewConfigServiceClient(srv.Client(), srv.URL)

	response, err := client.TestConnection(context.Background(), connect.NewRequest(&configpb.TestConnectionRequest{
		Section: "cyberhub", Config: &configpb.DistributeConfig{},
	}))
	if err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if len(response.Msg.Checks) != 1 || response.Msg.Checks[0].Name != "cyberhub" {
		t.Fatalf("expected one cyberhub check, got %+v", response.Msg.Checks)
	}

	_, err = client.TestConnection(context.Background(), connect.NewRequest(&configpb.TestConnectionRequest{
		Section: "agent", Config: &configpb.DistributeConfig{},
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
	resp, err := svc.TestConn(context.Background(), "ioa", configWith(func(c *configpb.DistributeConfig) {
		c.Ioa = &configpb.IOAConfig{Url: srv.URL, Token: "t"}
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c, ok := findCheck(resp, "ioa")
	if !ok || !c.OK {
		t.Fatalf("expected ioa ok, got %+v", resp)
	}
	if !strings.Contains(c.Detail, "1 space") {
		t.Fatalf("expected space count in detail, got %q", c.Detail)
	}
}
