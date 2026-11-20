package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSectionLookupDoesNotSealDeclarations(t *testing.T) {
	r := NewSections()
	handle, err := r.Add(Section{Key: "temporary", New: func() any { return &fixtureOptions{} }})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Has("temporary") || r.Has("missing") {
		t.Fatal("incorrect declaration lookup")
	}
	if err := handle.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.Has("temporary") {
		t.Fatal("lookup sealed the registry and prevented declaration rollback")
	}
	if _, err := r.Add(Section{Key: "replacement", New: func() any { return &fixtureOptions{} }}); err != nil {
		t.Fatalf("lookup prevented subsequent declarations: %v", err)
	}
}

func TestSectionAcceptsNull(t *testing.T) {
	r := fixtureSections(t)
	values, err := r.Normalize(map[string]any{"fixture": nil})
	if err != nil {
		t.Fatal(err)
	}
	value, err := r.Decode("fixture", values["fixture"])
	if err != nil || value.(*fixtureOptions).Count != 3 {
		t.Fatalf("null section lost defaults: %v %v", value, err)
	}
}

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
	_, err := r.Add(Section{Key: "fixture", Secrets: []string{"auth.token"},
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
	if err := os.WriteFile(path, []byte("fixture:\n  name: from-file\n  count: 12\n  enabled: true\n"), 0600); err != nil {
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

func TestDirectSectionDecodeRejectsNumericOverflow(t *testing.T) {
	type options struct {
		Signed   int8  `json:"signed"`
		Unsigned uint8 `json:"unsigned"`
		Large    int64 `json:"large"`
	}
	r := NewSections()
	if _, err := r.Add(Section{Key: "numbers", New: func() any { return &options{} }}); err != nil {
		t.Fatal(err)
	}
	for _, fields := range []map[string]any{
		{"signed": 128}, {"signed": float64(-129)}, {"unsigned": -1}, {"unsigned": float64(256)},
		{"large": uint64(1) << 63}, {"large": float64(uint64(1) << 63)}, {"signed": 1.5},
	} {
		if _, err := r.Decode("numbers", fields); err == nil {
			t.Fatalf("accepted overflowing numeric input: %v", fields)
		}
	}
	decoded, err := r.Decode("numbers", map[string]any{"signed": float64(127), "unsigned": 255, "large": int64(-1)})
	if err != nil || *decoded.(*options) != (options{Signed: 127, Unsigned: 255, Large: -1}) {
		t.Fatalf("valid numeric input = %v, %v", decoded, err)
	}
}

func TestExtensionConfigurationRejectsConflictsAndInvalidValues(t *testing.T) {
	r := fixtureSections(t)
	for _, fields := range []map[string]any{{"count": -1}, {"count": "bad"}, {"count": 1.5}, {"typo": true}} {
		if _, err := r.Decode("fixture", fields); err == nil {
			t.Fatalf("accepted %+v", fields)
		}
	}
	_, err := r.Normalize(map[string]any{"old_fixture": map[string]any{"count": 2}, "extensions": Values{"fixture": {"count": 3}}})
	if err == nil || !strings.Contains(err.Error(), "unknown configuration field") {
		t.Fatalf("obsolete format accepted: %v", err)
	}
	if _, err := r.Add(Section{Key: "fixture", New: func() any { return &fixtureOptions{} }}); err == nil {
		t.Fatal("duplicate key accepted")
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

func TestSnapshotValidatesOnceAndPreservesOwnedValues(t *testing.T) {
	calls := 0
	r := NewSections()
	if _, err := r.Add(Section{Key: "example", New: func() any { return &fixtureOptions{Count: 7, Enabled: true} }, Validate: func(any) error { calls++; return nil }, Environment: func(s Sources) (map[string]any, map[string]any, error) {
		value, _ := s.LookupEnv("EXAMPLE_NAME")
		return map[string]any{"name": value}, map[string]any{"count": 5}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	file, err := r.Normalize(map[string]any{"example": map[string]any{"count": 3}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.ResolveValues(file, Values{"example": {"count": 0, "enabled": false}}, func(string) (string, bool) { return "first", true })
	if err != nil {
		t.Fatal(err)
	}
	first, err := Get[*fixtureOptions](result, "example")
	if err != nil {
		t.Fatal(err)
	}
	if first.Count != 0 || first.Enabled || first.Name != "first" {
		t.Fatalf("precedence: %+v", first)
	}
	values := result.Values()
	values["example"]["name"] = "mutated"
	second, err := Get[*fixtureOptions](result, "example")
	if err != nil || second.Name != "first" || calls != 1 {
		t.Fatalf("snapshot leaked: %+v, calls %d, %v", second, calls, err)
	}
	if _, err := r.Add(Section{Key: "late", New: func() any { return &fixtureOptions{} }}); err == nil {
		t.Fatal("snapshot did not seal declarations")
	}
}

func TestSnapshotDoesNotDropUnencodableInput(t *testing.T) {
	r := fixtureSections(t)
	if _, err := r.ResolveValues(Values{"fixture": {"name": make(chan int)}}, nil, nil); err == nil {
		t.Fatal("invalid input was silently replaced by defaults")
	}
}

func TestDeclarationBatchCanRetractBeforeSeal(t *testing.T) {
	r := NewSections()
	handle, err := r.Add(Section{Key: "temporary", New: func() any { return &fixtureOptions{} }})
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.Keys()) != 0 {
		t.Fatalf("retracted sections = %v", r.Keys())
	}
}
