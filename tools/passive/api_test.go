package passive_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	passiveconfig "github.com/chainreactors/cyber/exts/passive/config"
	"github.com/chainreactors/cyber/tools/passive"
)

func TestEveryPassiveAPIUsesConfiguredBaseURL(t *testing.T) {
	for _, section := range passiveconfig.Sections() {
		t.Run(section.Key, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if !strings.HasPrefix(r.URL.Path, "/tenant/"+section.Key+"/") {
					t.Errorf("request escaped configured prefix: %s", r.URL.Path)
				}
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":"gateway rejected fixture query"}`)
			}))
			defer server.Close()
			credentials := map[string]string{"CENSYS_ORGANIZATION_ID": "organization"}
			if name := passiveconfig.Credential(section.Key); name != "" {
				credentials[name] = "private-key"
			}
			engine := passive.NewUncover(passive.Config{
				FofaKey: "private-key", HunterAPIKey: "private-key", Credentials: credentials, Keyless: []string{"shodan-idb"},
				BaseURLs: map[string]string{section.Key: server.URL + "/tenant/" + section.Key + "/"},
			})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			_, err := engine.Query(ctx, section.Key, "192.0.2.1")
			if calls.Load() == 0 || err == nil {
				t.Fatalf("upstream error did not reach caller: calls=%d err=%v", calls.Load(), err)
			}
			if strings.Contains(err.Error(), "private-key") {
				t.Fatalf("credential exposed: %v", err)
			}
		})
	}
}

func TestPassivePreservesMethodBodyAndAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/gateway/api/v3/search/quake_service" || r.Header.Get("X-QuakeToken") != "quake-key" {
			t.Errorf("upstream request changed: %s %s %v", r.Method, r.URL, r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["query"] != "example.test" {
			t.Errorf("upstream body changed: %v %v", body, err)
		}
		fmt.Fprint(w, `{"code":0,"data":[],"meta":{"pagination":{"count":0,"total":0}}}`)
	}))
	defer server.Close()
	engine := passive.NewUncover(passive.Config{Credentials: map[string]string{"QUAKE_TOKEN": "quake-key"}, BaseURLs: map[string]string{"quake": server.URL + "/gateway"}})
	if _, err := engine.Query(t.Context(), "quake", "example.test"); err != nil {
		t.Fatal(err)
	}
}

func TestPassiveGatewayDoesNotFollowRedirect(t *testing.T) {
	var escaped atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { escaped.Add(1) }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) }))
	defer server.Close()
	engine := passive.NewUncover(passive.Config{Keyless: []string{"shodan-idb"}, BaseURLs: map[string]string{"shodan-idb": server.URL}})
	if _, err := engine.Query(t.Context(), "shodan-idb", "192.0.2.1"); err == nil || escaped.Load() != 0 {
		t.Fatalf("redirect escaped upstream: err=%v calls=%d", err, escaped.Load())
	}
}
