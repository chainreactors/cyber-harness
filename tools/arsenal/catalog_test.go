package arsenal

import (
	"testing"

	crtm "github.com/chainreactors/crtm/pkg"
	"github.com/chainreactors/crtm/pkg/registry"
)

func TestHarnessCatalog(t *testing.T) {
	manager, err := NewManager(t.TempDir(), crtm.ManagerOption{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range manager.ListTools() {
		if seen[entry.Name] || entry.Version == "" || entry.Repo == "" || len(entry.Platforms) == 0 {
			t.Fatalf("incomplete or duplicate catalog entry: %+v", entry)
		}
		seen[entry.Name] = true
	}
	for _, test := range []struct{ name, os, asset, executable, tag string }{
		{"radare2", "windows", "r2blob-1.2.3-w64.zip", "r2blob.static.exe", "1.2.3"},
		{"capa", "windows", "capa-v1.2.3-windows.zip", "capa.exe", "v1.2.3"},
		{"capa", "linux", "capa-v1.2.3-linux.zip", "capa", "v1.2.3"},
		{"floss", "windows", "floss-v1.2.3-windows.zip", "floss.exe", "v1.2.3"},
		{"floss", "linux", "floss-v1.2.3-linux.zip", "floss", "v1.2.3"},
	} {
		entry, ok := manager.Catalog().Find(test.name)
		if !ok {
			t.Fatalf("harness tool %s missing at runtime", test.name)
		}
		asset, executable, err := entry.AssetFor("1.2.3", test.os, "amd64")
		if err != nil || asset != test.asset || executable != test.executable || entry.ReleaseTag("1.2.3") != test.tag {
			t.Fatalf("%s/%s: %s %s %v", test.name, test.os, asset, executable, err)
		}
		if _, _, err := entry.AssetFor(entry.Version, test.os, "arm64"); err == nil {
			t.Fatalf("%s claims unsupported arm64 artifact", test.name)
		}
	}
	// A distribution can still pin its own definition without changing Arsenal.
	override := registry.ToolEntry{Name: "capa", Version: "1.0.0", Repo: "example/capa"}
	manager, err = NewManager(t.TempDir(), crtm.ManagerOption{Catalog: []registry.ToolEntry{override}})
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := manager.Catalog().Find("capa")
	if entry.Version != override.Version || entry.Repo != override.Repo {
		t.Fatal("catalog overwrote distribution definition")
	}
}
