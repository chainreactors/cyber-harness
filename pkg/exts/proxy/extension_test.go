package proxy

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/extension"
)

func TestHubOwnsProxyLifecycle(t *testing.T) {
	ext, err := New(t.TempDir(), "", false, nil, config.TrafficOptions{})
	if err != nil {
		t.Fatal(err)
	}
	hub := ext.Hub()
	if hub.ProxyURL() != "" {
		t.Fatal("constructor started the proxy")
	}
	set, err := extension.New(extension.Entry{ID: "proxy", Extension: ext})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hub.ProxyURL() == "" {
		t.Fatal("extension did not start the proxy")
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := any(hub).(interface{ Close(context.Context) error }); ok {
		t.Fatal("proxy capability exposes lifecycle")
	}
}
