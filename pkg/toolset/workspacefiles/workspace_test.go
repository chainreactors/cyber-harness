package workspacefiles

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/pkg/extensions/toolgroup"
	"github.com/chainreactors/aiscan/pkg/toolset/registry"
)

func TestWorkspacePublishesCompleteProductFileSet(t *testing.T) {
	for _, includeList := range []bool{false, true} {
		tools := registry.New()
		definitions, err := Tools(t.TempDir(), nil, nil, includeList)
		if err != nil {
			t.Fatal(err)
		}
		instance, err := toolgroup.New(tools, definitions...)
		if err != nil {
			t.Fatal(err)
		}
		s, err := extension.New(extension.Entry{ID: "registry", Extension: tools}, extension.Entry{ID: "workspace", DependsOn: []string{"registry"}, Extension: instance})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := s.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
		if err := s.Load(t.Context()); err != nil {
			t.Fatal(err)
		}
		want := map[string]bool{"read": true, "write": true, "glob": true, "ls": includeList}
		got := make(map[string]bool)
		for _, definition := range tools.ToolDefinitions() {
			got[definition.Name] = true
		}
		for name, expected := range want {
			if got[name] != expected {
				t.Fatalf("includeList=%v tool %s = %v", includeList, name, got[name])
			}
		}
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(tools.ToolDefinitions()) != 0 {
			t.Fatal("workspace tools remained published")
		}
	}
}
