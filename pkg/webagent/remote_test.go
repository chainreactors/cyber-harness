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
		cfg.LLM.Provider = "deepseek"
		cfg.LLM.Model = "deepseek-chat"
		_ = json.NewEncoder(w).Encode(cfg)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	authURL := strings.Replace(server.URL, "http://", "http://reload-token@", 1)
	option, err := fetchRemoteConfig(authURL)
	if err != nil {
		t.Fatal(err)
	}
	if option.Provider != "deepseek" || option.Model != "deepseek-chat" {
		t.Fatalf("unexpected remote option: provider=%q model=%q", option.Provider, option.Model)
	}
}
