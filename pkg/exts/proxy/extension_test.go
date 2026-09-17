package proxy

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/namespaces"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/pkg/hosttest"
)

func TestHubOwnsProxyLifecycle(t *testing.T) {
	ext := New(Config{WorkDir: t.TempDir()})
	if ext.Hub() != nil {
		t.Fatal("the constructor started the proxy")
	}
	set, err := extension.New(namespaces.New(), hosttest.Provide[*hooks.Registry](hooks.New()), commands.NewRegistry(nil), ext)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ext.Hub() == nil || ext.Hub().ProxyURL() == "" {
		t.Fatal("extension did not start the proxy")
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := any(ext.Hub()).(interface{ Close(context.Context) error }); ok {
		t.Fatal("proxy capability exposes lifecycle")
	}
}
