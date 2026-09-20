package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	service "github.com/chainreactors/cyber/tools/ioa"
)

func TestServiceHandleDoesNotExposeLifecycle(t *testing.T) {
	adapter := New(service.Config{})
	hosttest.Load(t, t.Context(), extension.Provided(telemetry.NopLogger()), adapter)
	svc := adapter.Service()
	if svc == nil {
		t.Fatal("missing installed service")
	}
	if _, ok := any(svc).(interface{ Close(context.Context) error }); ok {
		t.Fatal("IOA service exposes Close")
	}
	if _, ok := any(svc).(interface{ Start(context.Context) error }); ok {
		t.Fatal("IOA service exposes Start")
	}
}

func TestExtensionPublishesCommandsBeforeRegistryActivation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	registry := coretool.NewCommandRegistry()
	ioa := New(service.Config{URL: server.URL, RegisterCommands: true})
	set, err := extension.New(
		extension.Provided(telemetry.NopLogger()), extension.Provided(hooks.New()),
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
