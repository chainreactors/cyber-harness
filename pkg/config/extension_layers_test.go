package config

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLayeredExtensionDeclarationContract(t *testing.T) {
	for _, mode := range []string{"defaults", "fallback", "files", "environment", "cli"} {
		t.Run(mode, func(t *testing.T) {
			c := isolatedContext(t)
			sections := NewSections()
			validations := 0
			_, err := sections.Add(Section{
				Key: "fixture", Aliases: []string{"old_fixture"}, Secrets: []string{"auth.token"},
				New: func() any { return &fixtureOptions{Name: "default", Count: 3, Enabled: true} },
				AliasFields: func(fields map[string]any) (map[string]any, error) {
					if value, ok := fields["legacy_count"]; ok {
						fields["count"] = value
						delete(fields, "legacy_count")
					}
					return fields, nil
				},
				Environment: func(s Sources) (map[string]any, map[string]any, error) {
					var overrides, fallbacks map[string]any
					if v, ok := s.LookupEnv("FIXTURE_NAME"); ok {
						overrides = map[string]any{"name": v}
					}
					if mode != "defaults" {
						fallbacks = map[string]any{"name": "fallback", "count": 5}
					}
					return overrides, fallbacks, nil
				},
				Normalize: func(v any) error {
					v.(*fixtureOptions).Name = strings.TrimSpace(v.(*fixtureOptions).Name)
					return nil
				},
				Validate: func(v any) error {
					validations++
					if v.(*fixtureOptions).Count < 0 {
						return fmt.Errorf("negative count")
					}
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			wantName, wantCount, wantEnabled := "default", 3, true
			if mode != "defaults" {
				wantName, wantCount = "fallback", 5
			}
			if mode == "files" || mode == "environment" || mode == "cli" {
				putConfig(t, c.UserFile(), "old_fixture:\n  name: user\n  legacy_count: 7\n  auth:\n    token: inherited-secret\n")
				putConfig(t, filepath.Join(c.Directory, DefaultConfigName), "extensions:\n  fixture:\n    name: ' project '\n    count: 0\n    enabled: false\n")
				wantName, wantCount, wantEnabled = "project", 0, false
			}
			if mode == "environment" || mode == "cli" {
				c.LookupEnv = func(name string) (string, bool) { return " env ", name == "FIXTURE_NAME" }
				wantName = "env"
			}
			option := &Option{Context: c, Sections: sections, Explicit: map[string]bool{}}
			if mode == "cli" {
				option.Extensions = Values{"fixture": {"name": "", "count": 0, "enabled": false}}
				wantName = ""
			}
			if _, err := ResolveRuntimeConfig(option); err != nil {
				t.Fatal(err)
			}
			value, err := Get[*fixtureOptions](option.Resolved, "fixture")
			if err != nil || value.Name != wantName || value.Count != wantCount || value.Enabled != wantEnabled || validations != 1 {
				t.Fatalf("resolved %+v, error %v, validations %d", value, err, validations)
			}
			if mode == "files" || mode == "environment" || mode == "cli" {
				if value.Auth.Token != "inherited-secret" {
					t.Fatal("project discarded inherited extension secret")
				}
				redacted := Redact(option.Snapshot.Effective, sections)
				if strings.Contains(fmt.Sprint(redacted), "inherited-secret") {
					t.Fatal("inspection exposed extension secret")
				}
				file, err := option.Snapshot.FileOptions(sections)
				if err != nil || file.Extensions["fixture"]["name"] != " project " {
					t.Fatalf("file view inherited runtime overrides: %v", err)
				}
			}
			value.Name = "mutated"
			again, err := Get[*fixtureOptions](option.Resolved, "fixture")
			if err != nil || again.Name != wantName || validations != 1 {
				t.Fatal("configuration reads mutated values or repeated validation")
			}
		})
	}
}

func TestLayeredExtensionRejectsInvalidDeclarations(t *testing.T) {
	for name, body := range map[string]string{
		"unknown field":  "extensions:\n  fixture:\n    typo: true\n",
		"invalid type":   "extensions:\n  fixture:\n    count: wrong\n",
		"invalid value":  "extensions:\n  fixture:\n    count: -1\n",
		"alias conflict": "old_fixture:\n  count: 2\nextensions:\n  fixture:\n    count: 3\n",
	} {
		t.Run(name, func(t *testing.T) {
			c := isolatedContext(t)
			putConfig(t, filepath.Join(c.Directory, DefaultConfigName), body)
			if _, err := ResolveRuntimeConfig(&Option{Context: c, Sections: fixtureSections(t)}); err == nil {
				t.Fatal("invalid extension configuration accepted")
			}
		})
	}
	c := isolatedContext(t)
	if _, err := ResolveRuntimeConfig(&Option{Context: c, Sections: fixtureSections(t), Extensions: Values{"undeclared": {"enabled": true}}}); err == nil {
		t.Fatal("undeclared CLI extension bypassed declaration registry")
	}
}

func TestLayeredExtensionEditsPreserveUnavailableConfiguration(t *testing.T) {
	c := isolatedContext(t)
	putConfig(t, c.UserFile(), "old_fixture:\n  name: user\n  auth:\n    token: user-secret\n")
	project := filepath.Join(c.Directory, DefaultConfigName)
	putConfig(t, project, "extensions:\n  fixture:\n    count: 0\n  unavailable:\n    credential: private\n    rules: [one, two]\n")
	sections := fixtureSections(t)
	s, err := LoadSnapshot(c, "", sections)
	if err != nil {
		t.Fatal(err)
	}
	before := s.RuntimeDocument(sections)
	after := CloneDocument(before)
	after["extensions"].(map[string]any)["fixture"].(map[string]any)["name"] = "edited"
	patched, err := s.ApplyChanges(before, after)
	if err != nil {
		t.Fatal(err)
	}
	extensions := patched["extensions"].(map[string]any)
	if !reflect.DeepEqual(extensions["unavailable"], s.TargetDocument()["extensions"].(map[string]any)["unavailable"]) {
		t.Fatal("edit changed unavailable extension")
	}
	if strings.Contains(fmt.Sprint(patched), "user-secret") || extensions["fixture"].(map[string]any)["name"] != "edited" {
		t.Fatal("edit copied inherited credentials or lost the change")
	}
}
