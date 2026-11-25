package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllSectionsUseFlatNamesWithoutRevalidation(t *testing.T) {
	type options struct {
		Count    int    `json:"count"`
		Endpoint string `json:"endpoint"`
	}
	sections := NewSections()
	validations := 0
	if _, err := sections.Add(Section{Key: "custom", New: func() any { return &options{} }, Secrets: []string{"endpoint"}, Validate: func(any) error { validations++; return nil }}); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "cyber.yaml")
	if err := os.WriteFile(path, []byte("custom:\n  count: 0\n  endpoint: https://example.test/private-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	option := &Option{Sections: sections, Explicit: map[string]bool{}, Context: &Context{Directory: directory, Home: t.TempDir(), LookupEnv: func(string) (string, bool) { return "", false }}}
	if _, err := ResolveRuntimeConfig(option); err != nil {
		t.Fatal(err)
	}
	if validations != 1 {
		t.Fatalf("display revalidated configuration: %d", validations)
	}
	if _, exists := option.Snapshot.Effective["extensions"]; exists {
		t.Fatal("flat display retained an empty envelope")
	}
	if strings.Contains(fmt.Sprint(Redact(option.Snapshot.Effective, sections)), "private-secret") {
		t.Fatal("canonical root view ignored declared secrets")
	}
	if _, err := sections.Normalize(map[string]any{"custom": map[string]any{"count": 0}, "extensions": map[string]any{"custom": map[string]any{"count": 1}}}); err == nil {
		t.Fatal("conflicting flat and nested values were accepted")
	}
}

func TestOnlyFlatConfigurationAccepted(t *testing.T) {
	sections := fixtureSections(t)
	for _, input := range []string{
		"extensions:\n  fixture:\n    count: 1\n",
		"old_fixture:\n  count: 1\n",
		"extensions: null\n",
		"unknown:\n  count: 1\n",
	} {
		option := &Option{Sections: sections}
		if err := LoadConfigBytes([]byte(input), option); err == nil {
			t.Fatalf("obsolete or unknown configuration accepted: %s", input)
		}
	}
}
