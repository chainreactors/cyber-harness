package proxy

import (
	"context"
	"github.com/chainreactors/cyber/core/egress"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/namespaces"
	coretool "github.com/chainreactors/cyber/core/tool"
)

func TestHubOwnsProxyLifecycle(t *testing.T) {
	ext := New(Config{WorkDir: t.TempDir()})
	// The routing endpoint is reached as a capability, so the borrow is what
	// proves the proxy started -- there is no accessor to peek through.
	var endpoint egress.Endpoint
	borrow := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		endpoint, err = extension.Use[egress.Endpoint](scope)
		return err
	}}
	set, err := extension.New(namespaces.New(), extension.Provided[*hooks.Registry](hooks.New()), coretool.NewCommandRegistry(), ext, borrow)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if endpoint == nil || endpoint.ProxyURL() == "" {
		t.Fatal("extension did not start the proxy")
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := any(endpoint).(interface{ Close(context.Context) error }); ok {
		t.Fatal("proxy capability exposes lifecycle")
	}
}
