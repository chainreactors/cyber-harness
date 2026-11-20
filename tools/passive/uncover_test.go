package passive

import (
	"slices"
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
