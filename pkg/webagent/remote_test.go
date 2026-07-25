package webagent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

func TestFetchRemoteConfigUsesBearerTokenFromURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/config/distribute", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer reload-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var cfg webproto.DistributeConfig
		cfg.LLM.ActiveProfile = "p1"
		cfg.LLM.Providers = []webproto.LLMProviderConfig{
			{ID: "p1", Provider: "deepseek", Model: "deepseek-chat", MaxTokens: 8192, ContextWindow: 128000},
			{ID: "p2", Provider: "openai", Model: "gpt-5"},
		}
		_ = json.NewEncoder(w).Encode(cfg)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	authURL := strings.Replace(server.URL, "http://", "http://reload-token@", 1)
	option, err := fetchRemoteConfig(authURL)
	if err != nil {
		t.Fatal(err)
	}
	if option.ActiveProfile != "p1" || len(option.Providers) != 2 {
		t.Fatalf("unexpected remote option: %+v", option.LLMOptions)
	}
	primary := option.Providers[0]
	if primary.Provider != "deepseek" || primary.Model != "deepseek-chat" {
		t.Fatalf("unexpected primary profile: %+v", primary)
	}
	if primary.MaxTokens != 8192 || primary.ContextWindow != 128000 {
		t.Fatalf("remote model limits were not propagated: %+v", primary)
	}
}
