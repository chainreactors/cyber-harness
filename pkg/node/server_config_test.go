package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	types "github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/harness"
	"github.com/chainreactors/cyber/pkg/profile"
	webservice "github.com/chainreactors/cyber/pkg/web/service"
)

// The server key exists only in its runtime, not in the editable config store.
type emptyServerConfigStore struct{}

func (emptyServerConfigStore) GetDistributeConfig(context.Context) (string, bool, *types.DistributeConfig, error) {
	return "", false, &types.DistributeConfig{}, nil
}
func (emptyServerConfigStore) PrepareDistributeConfig(context.Context, *types.DistributeConfig) (*webservice.PreparedConfig, error) {
	return nil, errors.New("read-only test store")
}
func (emptyServerConfigStore) CommitDistributeConfig(context.Context, *webservice.PreparedConfig) error {
	return errors.New("read-only test store")
}
func (emptyServerConfigStore) DiscardDistributeConfig(*webservice.PreparedConfig) {}

type serverConfigTestProfile struct{ *reloadTestProfile }

func (p *serverConfigTestProfile) AgentStatus() *aop.AgentStatus {
	providers, _ := p.Providers()
	return AgentStatus(providers)
}

func TestRemoteNodeUsesServerKeyWithoutLocalLLMConfig(t *testing.T) {
	testRemoteNodeUsesServerLLM(t, false)
}

func TestRemoteNodeUsesServerLLMDespiteLocalOverrides(t *testing.T) {
	testRemoteNodeUsesServerLLM(t, true)
}

func testRemoteNodeUsesServerLLM(t *testing.T, localOverrides bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	completion := make(chan struct{}, 1)
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer server-only-key" {
			http.Error(w, "server key required", http.StatusUnauthorized)
			return
		}
		var request struct {
			Stream bool   `json:"stream"`
			Model  string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Model != "server-model" {
			http.Error(w, "server model required", http.StatusBadRequest)
			return
		}
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"pong\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			select {
			case completion <- struct{}{}:
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`)
	}))
	defer llm.Close()

	h, err := harness.New(harness.Config{Base: harness.BaseConfig{
		Directory: t.TempDir(),
		Provider:  provider.StartupConfig{Mode: provider.StartupDisabled},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	if err := h.Load(ctx); err != nil {
		t.Fatal(err)
	}
	providers, err := h.Providers()
	if err != nil {
		t.Fatal(err)
	}
	providers.Set(nil, provider.ProviderConfig{Provider: "openai", APIKey: "server-only-key", Model: "server-model", BaseURL: llm.URL + "/v1"})
	svc := webservice.NewService(webservice.ServiceConfig{Profile: &reloadTestProfile{Harness: h}, ConfigStore: emptyServerConfigStore{}})
	defer svc.Close(context.Background())
	if result, err := svc.API().Config.TestLLM(ctx, &types.LLMProbeRequest{}); err != nil || !result.GetOk() || result.GetModel() != "server-model" {
		t.Fatalf("server runtime health probe = %v, error = %v", result, err)
	}
	pool := webservice.NewAgentPool(svc.Hub(), nil)
	defer pool.Close(context.Background())
	svc.SetAgentPool(pool)
	server := httptest.NewServer(svc.NodeWebSocketHandler())
	defer server.Close()

	directory := t.TempDir()
	builds := 0
	build := func(request profile.Request) (profile.Profile, error) {
		if builds == 0 && request.ProviderMode != profile.ProviderDisabled {
			return nil, fmt.Errorf("node initialized a model before receiving server configuration")
		}
		builds++
		h, err := harness.New(harness.Config{
			Base: harness.BaseConfig{
				Directory: directory,
				Provider:  provider.StartupConfig{Mode: request.ProviderMode, Config: cfg.ProviderConfig(request.Option)},
			},
			Session: request.Session,
		})
		if err != nil {
			return nil, err
		}
		return &serverConfigTestProfile{reloadTestProfile: &reloadTestProfile{Harness: h}}, nil
	}
	done := make(chan struct{})
	var nodeErr error
	option := &cfg.Option{
		Explicit: map[string]bool{},
		Context: &cfg.Context{
			Directory: directory, Home: directory, Executable: filepath.Join(directory, "node.exe"),
			LookupEnv: func(string) (string, bool) { return "", false },
		},
		NodeOptions:  cfg.NodeOptions{NodeID: "server-key-node"},
		AgentOptions: cfg.AgentOptions{ServerURL: server.URL, Prompt: "Say pong"},
	}
	if localOverrides {
		option.LLMOptions = cfg.LLMOptions{ActiveProfile: "stale-local", Provider: "openai", APIKey: "local-key", Model: "local-model", BaseURL: llm.URL + "/v1"}
		for _, flag := range []string{"profile", "provider", "api-key", "model", "base-url"} {
			option.MarkExplicit(flag)
		}
		option.Context.LookupEnv = func(key string) (string, bool) {
			value, ok := map[string]string{"CYBER_API_KEY": "env-key", "CYBER_MODEL": "env-model", "CYBER_PROVIDER": "invalid-local"}[key]
			return value, ok
		}
	}
	go func() {
		defer close(done)
		nodeErr = RunWebSocket(ctx, build, option, telemetry.NopLogger())
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("node did not stop")
		}
	}()
	select {
	case <-completion:
	case <-done:
		t.Fatalf("node exited before calling the model: %v", nodeErr)
	case <-ctx.Done():
		t.Fatal("node did not call the model using the server key")
	}
	// The activated profile reconnects and reports that its LLM is ready.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, node := range pool.List() {
			if node.GetStatus().GetProvider() == "openai" && node.GetStatus().GetModel() == "server-model" && node.GetStatus().GetConfigError() == "" {
				return
			}
		}
		select {
		case <-ticker.C:
		case <-done:
			t.Fatalf("node exited before reconnecting: %v", nodeErr)
		case <-ctx.Done():
			t.Fatal("node did not reconnect with the server LLM active")
		}
	}
}
