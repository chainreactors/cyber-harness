//go:build full

package engine

import "testing"

func TestMergeReconOptionsCredentials(t *testing.T) {
	base := ReconOptions{FofaKey: "oldkey", HunterAPIKey: "oldhunter"}
	got := mergeReconOptions(base, ReconOptions{FofaKey: "newkey", HunterAPIKey: "newhunter"})
	if got.FofaKey != "newkey" || got.HunterAPIKey != "newhunter" {
		t.Fatalf("merge failed: %#v", got)
	}
}

func TestMergeReconOptionsEmptyDoesNotOverwrite(t *testing.T) {
	base := ReconOptions{FofaKey: "keep", IngressProxy: "socks5://keep"}
	got := mergeReconOptions(base, ReconOptions{})
	if got.FofaKey != "keep" || got.IngressProxy != "socks5://keep" {
		t.Fatalf("empty merge overwrote: %#v", got)
	}
}

func TestNewUncoverEngineFofaKeyOnly(t *testing.T) {
	t.Setenv("FOFA_KEY", "")

	eng := NewUncoverEngine(ReconOptions{FofaKey: "modern-api-key"}, nil)
	if eng.keys.FofaKey != "modern-api-key" {
		t.Fatalf("key-only creds did not backfill FofaKey: got %q", eng.keys.FofaKey)
	}
	if !sourceAvailable(eng, "fofa") {
		t.Fatalf("fofa not available for key-only creds: %v", eng.Sources())
	}
}

func TestNewUncoverEngineDoesNotRereadCredentialEnvironment(t *testing.T) {
	t.Setenv("FOFA_KEY", "env-key")
	t.Setenv("HUNTER_API_KEY", "env-hunter")

	eng := NewUncoverEngine(ReconOptions{
		FofaKey:      "cli-key",
		HunterAPIKey: "cli-hunter",
	}, nil)
	if eng.keys.FofaKey != "cli-key" {
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
