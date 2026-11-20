package config

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/resource"
	"github.com/chainreactors/cyber/core/types"
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

func TestFofaBaseURLConfiguration(t *testing.T) {
	for _, baseURL := range []string{"", "https://gateway.test/fofa", "https://gateway.test/fofa/"} {
		value, err := FofaSection().Decode(map[string]any{"key": "gateway-key", "base_url": baseURL})
		if err != nil {
			t.Fatal(err)
		}
		want := baseURL
		if want == "" {
			want = "https://fofa.info"
		}
		if value.(*Fofa).BaseURL != want {
			t.Fatalf("base_url changed: got %q, want %q", value.(*Fofa).BaseURL, want)
		}
	}
	for _, baseURL := range []string{
		"gateway.test", "/fofa", "ftp://gateway.test", "https://key@gateway.test",
		"https://gateway.test?key=secret", "https://gateway.test?", "https://gateway.test#fragment",
	} {
		if _, err := FofaSection().Decode(map[string]any{"key": "gateway-key", "base_url": baseURL}); err == nil {
			t.Fatalf("invalid base_url accepted: %q", baseURL)
		}
	}
	if _, err := FofaSection().Decode(map[string]any{"key": "gateway-key", "url": "https://gateway.test/search"}); err == nil {
		t.Fatal("obsolete full endpoint configuration accepted")
	}
}

func TestHunterConnection(t *testing.T) {
	sections := cfg.NewSections()
	resources := resource.New()
	if _, err := resource.Define[cfg.Section](resources, sections); err != nil {
		t.Fatal(err)
	}
	if _, err := resource.Define[cfg.Connection](resources, sections.ConnectionPoint()); err != nil {
		t.Fatal(err)
	}
	if err := Declare(resources); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, reply, message string
		ok                   bool
	}{
		{"success", `{"code":200,"message":"success","data":{"total":7}}`, "7 total", true},
		{"error", `{"code":401,"message":"invalid api-key"}`, "invalid api-key", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("api-key") != "hunter-key" {
					t.Error("connection did not use configured key")
				}
				fmt.Fprint(w, test.reply)
			}))
			defer server.Close()
			previous := hunterSearchEndpoint
			hunterSearchEndpoint = server.URL
			t.Cleanup(func() { hunterSearchEndpoint = previous })
			extensions, err := cfg.ValuesToProto(cfg.Values{ConfigKey: {"hunter_api_key": "hunter-key"}})
			if err != nil {
				t.Fatal(err)
			}
			checks, err := sections.TestConnection(t.Context(), ConfigKey, &types.DistributeConfig{Extensions: extensions}, nil)
			if err != nil || len(checks) != 1 {
				t.Fatalf("connection results: %v, %v", checks, err)
			}
			check := checks[0]
			if check.Name != "hunter" || check.Ok != test.ok || !strings.Contains(check.Detail+check.Error, test.message) {
				t.Fatalf("connection result: %+v", check)
			}
		})
	}
}
