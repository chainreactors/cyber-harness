package passive

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestUncoverFofaKeyOnly(t *testing.T) {
	t.Setenv("FOFA_KEY", "")

	eng := NewUncover(Config{FofaKey: "modern-api-key"})
	if eng.keys.FofaKey != "modern-api-key" {
		t.Fatalf("key-only creds did not backfill FofaKey: got %q", eng.keys.FofaKey)
	}
	if !slices.Contains(eng.Sources(), "fofa") {
		t.Fatalf("fofa not available for key-only creds: %v", eng.Sources())
	}
}

func TestUncoverUsesResolvedCredentials(t *testing.T) {
	t.Setenv("FOFA_KEY", "env-key")
	t.Setenv("HUNTER_API_KEY", "env-hunter")

	eng := NewUncover(Config{
		FofaKey:      "cli-key",
		HunterAPIKey: "cli-hunter",
	})
	if eng.keys.FofaKey != "cli-key" {
		t.Fatalf("FOFA environment bypassed resolved config: %#v", eng.keys)
	}
	if eng.keys.HunterToken != "cli-hunter" {
		t.Fatalf("Hunter environment bypassed resolved config: %#v", eng.keys)
	}
}

func TestUncoverUsesInjectedCredentials(t *testing.T) {
	eng := NewUncover(Config{Credentials: map[string]string{
		"SHODAN_API_KEY": "shodan-key",
	}})
	if eng.keys.Shodan != "shodan-key" || !slices.Contains(eng.Sources(), "shodan") {
		t.Fatalf("injected provider key not applied: %#v", eng.keys)
	}
}

func TestUncoverKeylessSourcesRequireExplicitEnablement(t *testing.T) {
	if got := NewUncover(Config{}).Sources(); len(got) != 0 {
		t.Fatalf("unconfigured engine exposes sources: %v", got)
	}
	if got := NewUncover(Config{Keyless: []string{"shodan-idb"}}).Sources(); !slices.Equal(got, []string{"shodan-idb"}) {
		t.Fatalf("enabled keyless source = %v", got)
	}
	if got := NewUncover(Config{Keyless: []string{"shodan-idb"}, Disabled: []string{"shodan-idb"}}).Sources(); len(got) != 0 {
		t.Fatalf("disabled keyless source remained available: %v", got)
	}
}

func TestUncoverQueryRejectsUnconfiguredSources(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected request", http.StatusTeapot)
	}))
	defer server.Close()
	for _, config := range []Config{
		{},
		{Keyless: []string{"shodan-idb"}, Disabled: []string{"shodan-idb"}},
	} {
		config.BaseURLs = map[string]string{"shodan-idb": server.URL}
		if _, err := NewUncover(config).Query(t.Context(), "shodan-idb", "192.0.2.1"); err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Fatalf("unconfigured query err = %v", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("unconfigured source sent %d request(s)", calls.Load())
	}
}
