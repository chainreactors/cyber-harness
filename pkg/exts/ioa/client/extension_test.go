package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/commands"
	service "github.com/chainreactors/cyber/tools/ioa"
)

func TestRuntimeHandleDoesNotExposeLifecycle(t *testing.T) {
	adapter := New(service.Config{}, Dependencies{})
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
	ioa := New(service.Config{URL: server.URL, RegisterCommands: true}, Dependencies{})
	set, err := extension.New(
		registry,
		ioa,
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
