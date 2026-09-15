package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/capability"
)

func TestBundleIsExplicitAndLocalOverrideWins(t *testing.T) {
	const name = "extension-fixture"
	const location = "fixture://skills/extension-fixture/SKILL.md"
	bundle := Bundle{Skills: []Skill{{Name: name, Source: SourceBundle, Location: location}}, ReadVirtual: func(uri string) (string, bool, error) {
		if uri != location {
			return "", false, nil
		}
		return "---\nname: extension-fixture\ndescription: fixture\n---\nBundle body", true, nil
	}}
	absent, _ := LoadAll(nil, capability.Catalog{})
	if _, ok := absent.ByName(name); ok {
		t.Fatal("unselected bundle was loaded")
	}
	store, _ := LoadAll(nil, capability.Catalog{}, bundle)
	if store.ReadBody(name) != "Bundle body" {
		t.Fatal("bundle body was not resolved")
	}
	local := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(local, []byte("---\nname: extension-fixture\ndescription: fixture\n---\nLocal body"), 0600); err != nil {
		t.Fatal(err)
	}
	store, diags := LoadAll([]string{local}, capability.Catalog{}, bundle)
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %v", diags)
	}
	if store.ReadBody(name) != "Local body" {
		t.Fatal("local override did not win")
	}
	raw, handled, err := store.ReadVirtual(location)
	if err != nil || !handled || !strings.Contains(raw, "Bundle body") {
		t.Fatalf("explicit virtual source changed: %q %v %v", raw, handled, err)
	}
}
