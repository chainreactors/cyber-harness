//go:build record && cgo && (windows || linux)

package record

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/toolset"
)

func TestExtensionOwnsRecordTool(t *testing.T) {
	registry := toolset.NewRegistry(nil)
	instance, err := New(t.TempDir(), filepath.Join(t.TempDir(), "record"), 3)
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(registry, instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	definitions := registry.ToolDefinitions()
	if len(definitions) != 1 || definitions[0].Name != "record" {
		t.Fatalf("record definitions = %#v", definitions)
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if definitions := registry.ToolDefinitions(); len(definitions) != 0 {
		t.Fatalf("record tool survived Extension close: %#v", definitions)
	}
}

func TestExtensionRejectsInvalidConfiguration(t *testing.T) {
	if instance, err := New(t.TempDir(), t.TempDir(), 17); err == nil {
		_ = instance.Close(context.Background())
		t.Fatal("accepted invalid maximum")
	}
}
