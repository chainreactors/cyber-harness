package agent

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
	coretool "github.com/chainreactors/aiscan/core/tool"
	toolregistry "github.com/chainreactors/aiscan/pkg/toolset/registry"
)

var testToolOwner atomic.Uint64

func newTestTools(t testing.TB, tools ...coretool.Tool) *toolregistry.Registry {
	t.Helper()
	registry := toolregistry.New()
	set, err := extension.New(extension.Entry{ID: "registry", Extension: registry})
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	if len(tools) > 0 {
		addTestTools(t, registry, tools...)
	}
	return registry
}

func addTestTools(t testing.TB, registry *toolregistry.Registry, tools ...coretool.Tool) {
	t.Helper()
	owner := fmt.Sprintf("test.%d", testToolOwner.Add(1))
	if _, err := registry.Register(owner, tools...); err != nil {
		t.Fatal(err)
	}
}
