package provider

import (
	"context"
	"errors"
	"github.com/chainreactors/aiscan/agent/provider"
	"github.com/chainreactors/aiscan/core/extension"
	"testing"
)

func TestProviderRollbackKeepsOtherProfileState(t *testing.T) {
	var first, second provider.State
	second.Set(nil, provider.ProviderConfig{Model: "other"})
	resource, err := New(&first, provider.StartupConfig{Enabled: true, Optional: true, Config: provider.ProviderConfig{Provider: "unsupported"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Health().Error != "" {
		t.Fatal("constructor initialized provider")
	}
	failure := errors.New("later extension failed")
	set, err := extension.New(extension.Entry{ID: "provider", Extension: resource}, extension.Entry{ID: "later", DependsOn: []string{"provider"}, Extension: extension.Func{LoadFunc: func(*extension.Scope) error { return failure }}})
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); !errors.Is(err, failure) {
		t.Fatalf("load: %v", err)
	}
	if p, c := first.Current(); p != nil || c.Provider != "" || first.Health().Error != "" {
		t.Fatal("rollback retained provider state")
	}
	if _, c := second.Current(); c.Model != "other" {
		t.Fatal("rollback touched another profile")
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
