//go:build full

package engine

import "testing"

func TestMergeReconOptionsFofaFields(t *testing.T) {
	base := ReconOptions{FofaEmail: "old@example.com", FofaKey: "oldkey"}
	got := mergeReconOptions(base, ReconOptions{FofaEmail: "new@example.com", FofaKey: "newkey"})
	if got.FofaEmail != "new@example.com" || got.FofaKey != "newkey" {
		t.Fatalf("merge failed: %#v", got)
	}
}

func TestMergeReconOptionsEmptyDoesNotOverwrite(t *testing.T) {
	base := ReconOptions{FofaEmail: "keep@example.com", IngressProxy: "socks5://keep"}
	got := mergeReconOptions(base, ReconOptions{})
	if got.FofaEmail != "keep@example.com" || got.IngressProxy != "socks5://keep" {
		t.Fatalf("empty merge overwrote: %#v", got)
	}
}

// FOFA simplified auth (2023+): only the API key is required. A key-only
// credential must register fofa as available without an email.
func TestNewUncoverEngineFofaKeyOnly(t *testing.T) {
	t.Setenv("FOFA_EMAIL", "")
	t.Setenv("FOFA_KEY", "")

	eng := NewUncoverEngine(ReconOptions{FofaKey: "modern-api-key"}, nil)
	if eng.keys.FofaKey != "modern-api-key" {
		t.Fatalf("key-only creds did not backfill FofaKey: got %q", eng.keys.FofaKey)
	}
	if !sourceAvailable(eng, "fofa") {
		t.Fatalf("fofa not available for key-only creds: %v", eng.Sources())
	}
}

// Legacy "email:key" credentials must keep working.
func TestNewUncoverEngineFofaLegacyEmailKey(t *testing.T) {
	t.Setenv("FOFA_EMAIL", "")
	t.Setenv("FOFA_KEY", "")

	eng := NewUncoverEngine(ReconOptions{FofaEmail: "a@b.com", FofaKey: "legacykey"}, nil)
	if eng.keys.FofaEmail != "a@b.com" || eng.keys.FofaKey != "legacykey" {
		t.Fatalf("legacy email:key not parsed: %#v", eng.keys)
	}
	if !sourceAvailable(eng, "fofa") {
		t.Fatalf("fofa not available for legacy creds: %v", eng.Sources())
	}
}

func TestNewUncoverEngineDoesNotRereadCredentialEnvironment(t *testing.T) {
	t.Setenv("FOFA_EMAIL", "env@example.com")
	t.Setenv("FOFA_KEY", "env-key")
	t.Setenv("HUNTER_API_KEY", "env-hunter")

	eng := NewUncoverEngine(ReconOptions{
		FofaEmail:    "cli@example.com",
		FofaKey:      "cli-key",
		HunterAPIKey: "cli-hunter",
	}, nil)
	if eng.keys.FofaEmail != "cli@example.com" || eng.keys.FofaKey != "cli-key" {
		t.Fatalf("FOFA environment bypassed resolved config: %#v", eng.keys)
	}
	if eng.keys.HunterToken != "cli-hunter" {
		t.Fatalf("Hunter environment bypassed resolved config: %#v", eng.keys)
	}
}

func TestNewUncoverEngineUsesInjectedCredentials(t *testing.T) {
	eng := NewUncoverEngine(ReconOptions{Credentials: map[string]string{
		"SHODAN_API_KEY": "shodan-key",
	}}, nil)
	if eng.keys.Shodan != "shodan-key" || !sourceAvailable(eng, "shodan") {
		t.Fatalf("injected provider key not applied: %#v", eng.keys)
	}
}

func sourceAvailable(e *UncoverEngine, name string) bool {
	for _, s := range e.Sources() {
		if s == name {
			return true
		}
	}
	return false
}
