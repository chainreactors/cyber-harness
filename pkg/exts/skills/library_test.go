package skills

import (
	"context"
	"github.com/chainreactors/aiscan/core/extension"
	"os"
	"path/filepath"
	"testing"
)

func TestLibrariesUseTheirProfileDirectories(t *testing.T) {
	libraries := make([]*Library, 2)
	for index, name := range []string{"one", "two"} {
		directory := t.TempDir()
		path := filepath.Join(directory, ".agent", "skills", name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("---\nname: "+name+"\ndescription: fixture\n---\nbody"), 0600); err != nil {
			t.Fatal(err)
		}
		resource, err := NewLibrary(LibraryConfig{Directory: directory})
		if err != nil {
			t.Fatal(err)
		}
		if len(resource.Store().Skills) != 0 {
			t.Fatal("constructor loaded skills")
		}
		set, err := extension.New(extension.Entry{ID: "skills", Extension: resource})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := set.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
		if err := set.Load(t.Context()); err != nil {
			t.Fatal(err)
		}
		libraries[index] = resource
		if _, found := resource.Store().ByName(name); !found {
			t.Fatalf("missing profile skill %s", name)
		}
	}
	if _, found := libraries[0].Store().ByName("two"); found {
		t.Fatal("second profile contaminated first")
	}
	if _, found := libraries[1].Store().ByName("one"); found {
		t.Fatal("first profile contaminated second")
	}
}
