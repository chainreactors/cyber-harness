package ioa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/pkg/commands"
	service "github.com/chainreactors/aiscan/tools/ioa"
)

func TestCommandSelectionRequiresRegistry(t *testing.T) {
	if _, err := New(service.Config{RegisterCommands: true}, nil, nil); err == nil {
		t.Fatal("accepted IOA command publication without a registry")
	}
	if _, err := New(service.Config{}, nil, nil); err != nil {
		t.Fatalf("dormant IOA service requires no registry: %v", err)
	}
}

func TestRuntimeHandleDoesNotExposeLifecycle(t *testing.T) {
	adapter, err := New(service.Config{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime := adapter.Runtime()
	if _, ok := any(runtime).(interface{ Close(context.Context) error }); ok {
		t.Fatal("IOA runtime exposes Close")
	}
	if _, ok := any(runtime).(interface{ Start(context.Context) error }); ok {
		t.Fatal("IOA runtime exposes Start")
	}
}

func TestExtensionPublishesCommandsBeforeRegistryActivation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	registry := commands.NewRegistry(nil)
	ioa, err := New(service.Config{URL: server.URL, RegisterCommands: true}, registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(
		extension.Entry{ID: "ioa", Extension: ioa},
		extension.Entry{ID: "command-registry", DependsOn: []string{"ioa"}, Extension: registry},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !registry.Has("ioa") {
		t.Fatal("IOA commands were not published")
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if registry.Has("ioa") || len(registry.Names()) != 0 {
		t.Fatal("closed composition still published IOA commands")
	}
}
