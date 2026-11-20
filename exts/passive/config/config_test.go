package config

import (
	"testing"

	cfg "github.com/chainreactors/cyber/pkg/config"
)

func resolveSections(t *testing.T, file, cli cfg.Values, env map[string]string) *cfg.Resolved {
	t.Helper()
	registry := cfg.NewSections()
	if _, err := registry.Add(Section()); err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.ResolveValues(file, cli, func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestReconSectionEnvironmentAndExplicitLimit(t *testing.T) {
	resolved := resolveSections(t,
		nil,
		cfg.Values{ConfigKey: map[string]any{"hunter_api_key": "cli-key", "limit": 0}},
		map[string]string{"HUNTER_API_KEY": "hunter"},
	)
	value, err := cfg.Get[*Recon](resolved, ConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	if value.HunterAPIKey != "cli-key" {
		t.Fatalf("hunter_api_key = %q, want CLI over env", value.HunterAPIKey)
	}
	if value.Limit == nil || *value.Limit != 0 {
		t.Fatalf("explicit zero limit lost: %#v", value.Limit)
	}
}

func TestReconSectionPreservesExplicitZeroLimitFromFile(t *testing.T) {
	resolved := resolveSections(t, cfg.Values{ConfigKey: map[string]any{"limit": 0}}, nil, nil)
	value, err := cfg.Get[*Recon](resolved, ConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	if value.Limit == nil || *value.Limit != 0 {
		t.Fatalf("file explicit zero limit lost: %#v", value.Limit)
	}
}

func TestUncoverCredentialsFromEnvironment(t *testing.T) {
	values := map[string]string{"SHODAN_API_KEY": "shodan-key", "CENSYS_API_TOKEN": "  "}
	credentials := UncoverCredentials(func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	})
	if len(credentials) != 1 || credentials["SHODAN_API_KEY"] != "shodan-key" {
		t.Fatalf("credentials = %#v", credentials)
	}
	if UncoverCredentials(nil) != nil {
		t.Fatal("nil lookup must yield nil credentials")
	}
}

func TestReconRejectsRemovedFofaKey(t *testing.T) {
	registry := cfg.NewSections()
	if _, err := registry.Add(Section()); err != nil {
		t.Fatal(err)
	}
	option := &cfg.Option{Sections: registry}
	if err := cfg.LoadConfigBytes([]byte("recon:\n  fofa_key: obsolete\n"), option); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ResolveValues(option.Extensions, nil, nil); err == nil {
		t.Fatal("removed recon.fofa_key accepted")
	}
	resolved := resolveSections(t, nil, nil, map[string]string{"FOFA_KEY": "obsolete"})
	if _, exists := resolved.Values()[ConfigKey]["fofa_key"]; exists {
		t.Fatal("FOFA_KEY restored removed configuration")
	}
}
