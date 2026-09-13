// Package proxy adapts proxy resources to the product extension lifecycle.
package proxy

import (
	"context"

	"github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	proxytool "github.com/chainreactors/aiscan/tools/proxy"
)

// Extension owns one proxy Resource. Consumers receive ProxyHub, whose type has no
// lifecycle methods; the extension graph alone starts and closes the resource.
type Extension struct {
	resource *proxytool.Resource
}

func New(workDir, originalProxy string, capture bool, registry *hooks.Registry, storage cfg.TrafficOptions) (*Extension, error) {
	resource, err := proxytool.NewHub(workDir, originalProxy, capture, registry, storage)
	if err != nil {
		return nil, err
	}
	return &Extension{resource: resource}, nil
}

// Hub returns the loaded extension's borrow-only routing/query capability. It
// intentionally has no lifecycle methods.
func (e *Extension) Hub() *proxytool.ProxyHub {
	if e == nil || e.resource == nil {
		return nil
	}
	return e.resource.ProxyHub
}

// RegisterNamespaces installs this extension's resource-control protocols on
// one connection. The connection owns the registration lifetime; Hub continues
// to own the underlying proxy state and storage.
func (e *Extension) RegisterNamespaces(mux *aop.NamespaceMux) error {
	return proxytool.RegisterTrafficNamespace(mux, e.Hub())
}

func (e *Extension) Load(scope *extension.Scope) error {
	return e.resource.Start(scope.Init())
}

func (e *Extension) Close(ctx context.Context) error {
	return e.resource.Close(ctx)
}

var _ extension.Extension = (*Extension)(nil)
