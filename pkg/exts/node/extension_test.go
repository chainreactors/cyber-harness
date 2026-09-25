package node

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/namespaces"
)

type registryConsumer struct {
	registry *namespaces.Registry
}

func (c *registryConsumer) Load(scope *extension.Scope) error {
	var err error
	c.registry, err = extension.Use[*namespaces.Registry](scope)
	return err
}

func TestExtensionPublishesNamespaceRegistry(t *testing.T) {
	consumer := &registryConsumer{}
	set, err := extension.New(New(), consumer)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if consumer.registry == nil {
		t.Fatal("node extension did not publish a namespace registry")
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
