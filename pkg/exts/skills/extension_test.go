package skills

import (
	"context"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/internal/extensiontest"
	fileext "github.com/chainreactors/aiscan/pkg/exts/files"
	"github.com/chainreactors/aiscan/pkg/toolset"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/chainreactors/aiscan/tools/files"
)

func TestMountDiscoveryReadAndClose(t *testing.T) {
	f, _ := fileext.New(toolset.NewRegistry(nil), nil, files.Config{Directory: t.TempDir()})
	fSet := extensiontest.Load(t, t.Context(), f)
	defer fSet.Close(context.Background())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("local instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	access := f.Files()
	m, err := New(access, dir)
	if err != nil {
		t.Fatal(err)
	}
	mSet := extensiontest.Set(t, extension.Entry{ID: "skills", Extension: m})
	defer mSet.Close(context.Background())
	if m.root != nil || len(m.Catalog().Locations()) != 0 {
		t.Fatal("constructor published resources")
	}
	if err := mSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := mSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	names := m.Catalog().Locations()
	if len(names) != 1 || names[0] != "skill://SKILL.md" {
		t.Fatalf("locations: %v", names)
	}
	names[0] = "mutated"
	data, err := access.Read(t.Context(), m.Catalog().Locations()[0])
	if err != nil || string(data) != "local instructions" {
		t.Fatalf("mounted read: %q %v", data, err)
	}
	if err := mSet.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := access.Read(t.Context(), "skill://SKILL.md"); err == nil {
		t.Fatal("read closed mount")
	}
	if len(m.Catalog().Locations()) != 0 {
		t.Fatal("closed mount still advertised")
	}
	if err := access.Write(t.Context(), "note", []byte("still usable")); err != nil {
		t.Fatal(err)
	}
}

func TestFailedMountCannotUnmountAnotherOwnerOrRetry(t *testing.T) {
	f, _ := fileext.New(toolset.NewRegistry(nil), nil, files.Config{Directory: t.TempDir()})
	fSet := extensiontest.Load(t, t.Context(), f)
	defer fSet.Close(context.Background())
	access := f.Files()
	if err := access.Mount("skill://", fstest.MapFS{"existing": &fstest.MapFile{Data: []byte("owned")}}); err != nil {
		t.Fatal(err)
	}
	m, _ := New(access, t.TempDir())
	mSet := extensiontest.Set(t, extension.Entry{ID: "skills", Extension: m})
	if err := mSet.Load(t.Context()); err == nil {
		t.Fatal("accepted conflicting mount")
	}
	firstRoot := m.root
	if err := mSet.Load(t.Context()); err == nil {
		t.Fatalf("retried failed mount: %v", err)
	}
	if m.root != firstRoot {
		t.Fatal("retry replaced owned root")
	}
	if err := mSet.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if m.root != nil {
		t.Fatal("failed Load root leaked")
	}
	data, err := access.Read(t.Context(), "skill://existing")
	if err != nil || string(data) != "owned" {
		t.Fatalf("failed instance removed another owner: %q %v", data, err)
	}
}
