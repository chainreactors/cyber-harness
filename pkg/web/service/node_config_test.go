package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent/provider"
	types "github.com/chainreactors/cyber/core/types"
	configpkg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/profile"
	"google.golang.org/protobuf/proto"
)

func TestNodeEnrollmentReceivesEffectiveLLMConfig(t *testing.T) {
	for _, source := range []string{"file", "environment", "cli", "runtime-only"} {
		t.Run(source, func(t *testing.T) {
			directory := t.TempDir()
			stored := configForModel("file-model")
			stored.Llm.Providers[0].ApiKey = "file-key"
			stored.Llm.Providers = append(stored.Llm.Providers, &types.LLMProviderConfig{Id: "secondary", Provider: "anthropic", Model: "other-model", ApiKey: "other-key"})
			option := &configpkg.Option{
				Context: &configpkg.Context{
					Directory: directory, Home: directory, Executable: filepath.Join(directory, "server.exe"),
					LookupEnv: func(string) (string, bool) { return "", false },
				},
				Explicit: map[string]bool{},
			}
			switch source {
			case "environment", "runtime-only":
				values := map[string]string{
					"CYBER_API_KEY": "env-key", "CYBER_MODEL": "env-model", "CYBER_BASE_URL": "https://env.example/v1",
				}
				option.Context.LookupEnv = func(key string) (string, bool) {
					value, ok := values[key]
					return value, ok
				}
			case "cli":
				option.LLMOptions = configpkg.LLMOptions{APIKey: "cli-key", Model: "cli-model", BaseURL: "https://cli.example/v1"}
				option.Explicit = map[string]bool{"api-key": true, "model": true, "base-url": true}
			}
			if source == "runtime-only" {
				stored = &types.DistributeConfig{}
			}
			resolved, err := configpkg.ResolveDistributedRuntime(stored, option)
			if err != nil {
				t.Fatal(err)
			}
			want := configpkg.ProviderConfig(resolved)
			current, providers, _ := newRecordingProfile(t)
			providers.Set(nil, want)
			before := proto.CloneOf(stored)
			svc := NewService(ServiceConfig{Profile: current, ConfigStore: &transactionalConfigStore{cfg: stored}})
			defer svc.Close(context.Background())
			pool := NewAgentPool(svc.Hub(), nil)
			defer pool.Close(context.Background())
			svc.SetAgentPool(pool)
			mux := http.NewServeMux()
			mux.Handle(NodeWebSocketPath, svc.NodeWebSocketHandler())
			server := httptest.NewServer(mux)
			defer server.Close()

			// Exercise both initial enrollment and reconnect over the real wire.
			for range 2 {
				conn := dialAgent(t, server, "llm-sync", nil)
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				message := unwrapEnvelope(t, readHubEnvelope(t, conn))
				reload, ok := message.(*types.ReloadProtocolMessage)
				if !ok || reload.GetRequest().GetConfig() == nil {
					t.Fatalf("expected enrollment config, got %T", message)
				}
				// A node with no local key must resolve the complete server provider.
				nodeOption, err := configpkg.ResolveDistributedRuntime(reload.GetRequest().GetConfig(), &configpkg.Option{
					Context: &configpkg.Context{
						Directory: directory, Home: directory, Executable: filepath.Join(directory, "node.exe"),
						LookupEnv: func(string) (string, bool) { return "", false },
					},
					Explicit: map[string]bool{},
				})
				if err != nil {
					t.Fatal(err)
				}
				if got := configpkg.ProviderConfig(nodeOption); !reflect.DeepEqual(got, want) {
					t.Fatalf("node provider = %+v, want %+v", got, want)
				}
				if source != "runtime-only" && !proto.Equal(reload.GetRequest().GetConfig().Llm.Providers[1], before.Llm.Providers[1]) {
					t.Fatal("inactive provider changed")
				}
				waitAgents(t, pool, 1)
				_ = conn.Close()
				waitAgents(t, pool, 0)
			}
			if !proto.Equal(stored, before) {
				t.Fatal("runtime credentials mutated stored configuration")
			}
		})
	}
}

func TestSaveConfigBroadcastsEffectiveLLMConfig(t *testing.T) {
	store := &transactionalConfigStore{cfg: configForModel("old-model")}
	next, providers, _ := newRecordingProfile(t)
	want := provider.ProviderConfig{
		Provider: "anthropic", Model: "runtime-model", APIKey: "runtime-key", BaseURL: "https://runtime.example",
		Timeout: 45, Images: proto.Bool(false), MaxTokens: 512, ContextWindow: 8192,
	}
	providers.Set(nil, want)
	svc := NewService(ServiceConfig{
		ConfigStore:  store,
		BuildProfile: func(context.Context, *PreparedConfig) (profile.Profile, error) { return next, nil },
	})
	defer svc.Close(context.Background())
	pool := NewAgentPool(svc.Hub(), nil)
	svc.SetAgentPool(pool)
	agent, sent := newFakeAgent("llm-sync", 1)
	pool.register(agent)
	incoming := configForModel("saved-model")
	before := proto.CloneOf(incoming)
	if _, err := svc.SaveConfig(t.Context(), incoming); err != nil {
		t.Fatal(err)
	}
	message := unwrapEnvelope(t, <-sent).(*types.ReloadProtocolMessage)
	distributed := message.GetRequest().GetConfig()
	if got := configpkg.ProviderConfigFromProto(distributed.GetLlm()); !reflect.DeepEqual(got, want) {
		t.Fatalf("broadcast provider = %+v, want %+v", got, want)
	}
	reconnected, err := pool.config(t.Context())
	if err != nil || !proto.Equal(reconnected, distributed) {
		t.Fatalf("reconnect differs from broadcast: %v", err)
	}
	if !proto.Equal(store.cfg, before) {
		t.Fatal("runtime credentials were persisted")
	}
}
