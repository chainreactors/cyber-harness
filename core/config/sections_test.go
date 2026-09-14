package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fixtureOptions struct {
	Name    string `json:"name"`
	Count   int    `json:"count"`
	Enabled bool   `json:"enabled"`
	Auth    struct {
		Token string `json:"token"`
	} `json:"auth"`
}

func fixtureSections(t *testing.T) *Sections {
	t.Helper()
	r := NewSections()
	err := r.Register("fixture", Section{Key: "fixture", Aliases: []string{"old_fixture"}, Secrets: []string{"auth.token"},
		New: func() any { return &fixtureOptions{Name: "default", Count: 3, Enabled: true} },
		Validate: func(value any) error {
			if value.(*fixtureOptions).Count < 0 {
				return fmt.Errorf("count must be non-negative")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestExtensionConfigurationPresenceAndRoundTrip(t *testing.T) {
	r := fixtureSections(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("old_fixture:\n  name: from-file\n  count: 12\n  enabled: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	values, err := r.Resolve(path, Values{"fixture": {"name": "", "count": 0, "enabled": false}})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := ValuesToProto(values)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := r.Decode("fixture", ValuesFromProto(wire)["fixture"])
	if err != nil {
		t.Fatal(err)
	}
	got := decoded.(*fixtureOptions)
	if got.Name != "" || got.Count != 0 || got.Enabled {
		t.Fatalf("explicit zeros lost: %+v", got)
	}
	defaults, err := r.Decode("fixture", nil)
	if err != nil || defaults.(*fixtureOptions).Count != 3 {
		t.Fatalf("default = %+v, %v", defaults, err)
	}
	if _, err := NewSections().Resolve(path, Values{"fixture": {}}); err == nil {
		t.Fatal("unselected extension accepted")
	}
}

func TestExtensionConfigurationRejectsConflictsAndInvalidValues(t *testing.T) {
	r := fixtureSections(t)
	for _, fields := range []map[string]any{{"count": -1}, {"count": "bad"}, {"typo": true}} {
		if _, err := r.Decode("fixture", fields); err == nil {
			t.Fatalf("accepted %+v", fields)
		}
	}
	_, err := r.Normalize(map[string]any{"old_fixture": map[string]any{"count": 2}, "extensions": Values{"fixture": {"count": 3}}})
	if err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("conflict = %v", err)
	}
	if err := r.Register("fixture", Section{Key: "fixture", New: func() any { return &fixtureOptions{} }}); err == nil {
		t.Fatal("duplicate key accepted")
	}
	if err := r.Register("fixture", Section{Key: "other", Aliases: []string{"old_fixture"}, New: func() any { return &fixtureOptions{} }}); err == nil {
		t.Fatal("duplicate alias accepted")
	}
}

func TestExtensionConfigurationMasksAndPreservesNestedSecrets(t *testing.T) {
	r := fixtureSections(t)
	current := Values{"fixture": {"name": "saved", "auth": map[string]any{"token": "sensitive"}}}
	view, secrets := r.View(current)
	if _, found := view["fixture"]["auth"].(map[string]any)["token"]; found {
		t.Fatal("secret exposed")
	}
	if !reflect.DeepEqual(secrets["fixture"], []string{"auth.token"}) {
		t.Fatalf("secret metadata = %v", secrets)
	}
	if !reflect.DeepEqual(r.Preserve(nil, current), current) {
		t.Fatal("omitted section lost")
	}
	for _, fields := range []map[string]any{{"name": "edited"}, {"auth": map[string]any{"token": ""}}} {
		out := r.Preserve(Values{"fixture": fields}, current)
		if out["fixture"]["auth"].(map[string]any)["token"] != "sensitive" {
			t.Fatal("secret lost")
		}
	}
	if current["fixture"]["auth"].(map[string]any)["token"] != "sensitive" {
		t.Fatal("source mutated")
	}
}
