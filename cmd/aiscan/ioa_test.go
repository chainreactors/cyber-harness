package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/extension"
	serverext "github.com/chainreactors/aiscan/pkg/exts/ioa/server"
	ioatools "github.com/chainreactors/aiscan/tools/ioa"
	ioaservice "github.com/chainreactors/aiscan/tools/ioa/server"
	ioaclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/ioa/protocols"
)

func TestProfileWithoutIOAHasNoCollaborationContributions(t *testing.T) {
	product, err := newAIScanProfile(minimalConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer product.Close(context.Background())
	if err := product.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	application, err := product.App()
	if err != nil {
		t.Fatal(err)
	}
	if application.Commands.Has("ioa") || application.Skills.ReadBody("ioa") != "" || application.Skills.ReadBody("checkpoint") != "" {
		t.Fatal("unselected client contributed commands or skills")
	}
	if product.ConsoleBindings() != nil || len(product.Capabilities()) != 0 || product.AgentStatus().Bound {
		t.Fatal("unselected client exposed host capabilities")
	}
}

func TestServerOutlivesClientProfileReplacement(t *testing.T) {
	serverExtension := serverext.New(ioaservice.Config{AccessKey: "test-key"})
	host, err := extension.New(extension.Entry{ID: "ioa-server", Extension: serverExtension})
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	httpServer := httptest.NewServer(serverExtension.Server().Handler())
	defer httpServer.Close()
	url := strings.Replace(httpServer.URL, "http://", "http://test-key@", 1)
	observer, err := ioaclient.NewClient(url, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := observer.EnsureRegistered(t.Context(), "observer", "", nil); err != nil {
		t.Fatal(err)
	}
	space, err := observer.Space(t.Context(), "persistent", "test")
	if err != nil {
		t.Fatal(err)
	}
	original, err := observer.Send(t.Context(), space.ID, protocols.SendMessage{Content: map[string]any{"text": "retained"}})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		config := minimalConfig(nil)
		config.IOA = &ioatools.Config{URL: url, NodeName: "client", Space: "persistent", AutoRegister: true, RegisterCommands: true}
		product, err := newAIScanProfile(config)
		if err != nil {
			t.Fatal(err)
		}
		if product.AgentStatus().Bound || product.ConsoleBindings() != nil {
			t.Fatal("unpublished profile exposed IOA state")
		}
		if err := product.Load(t.Context()); err != nil {
			t.Fatal(err)
		}
		app, err := product.App()
		if err != nil {
			t.Fatal(err)
		}
		if !app.Commands.Has("ioa") || app.Skills.ReadBody("checkpoint") == "" {
			t.Fatal("client contribution is incomplete")
		}
		if !product.AgentStatus().Bound {
			t.Fatal("bound client status missing")
		}
		reader := product.ioa
		if reader == nil {
			t.Fatal("loaded profile did not publish its query capability")
		}
		spaces, err := reader.ListSpaces(t.Context())
		if err != nil || len(spaces) != 1 || spaces[0].ID != space.ID {
			t.Fatalf("profile queries = %v, %v", spaces, err)
		}
		if err := product.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if product.AgentStatus().Bound || product.ConsoleBindings() != nil {
			t.Fatal("closed profile exposed IOA state")
		}
		if _, err := reader.ListSpaces(t.Context()); err == nil {
			t.Fatal("retained query capability accepted a request after profile Close")
		}
		messages, err := observer.Read(t.Context(), space.ID, protocols.ReadOptions{All: true})
		if err != nil || len(messages) != 1 || messages[0].ID != original.ID {
			t.Fatalf("host state or identity lost: %#v, %v", messages, err)
		}
	}
}

func TestManagedHTTPShutdownCancelsSSEBeforeDraining(t *testing.T) {
	owner := serverext.New(ioaservice.Config{AccessKey: "test-key"})
	set, err := extension.New(extension.Entry{ID: "ioa-server", Extension: owner})
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	srv := &http.Server{Handler: owner.Server().Handler()}
	defer srv.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- serveManagedHTTP(ctx, srv, listener, set.Close) }()
	client, err := ioaclient.NewClient("http://test-key@"+listener.Addr().String(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureRegistered(t.Context(), "streaming-client", "", nil); err != nil {
		t.Fatal(err)
	}
	space, err := client.Space(t.Context(), "stream", "test")
	if err != nil {
		t.Fatal(err)
	}
	_, _, stop, err := client.Subscribe(t.Context(), space.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP shutdown waited for an uncanceled SSE handler")
	}
	if set.Active() {
		t.Fatal("server was still published after HTTP shutdown")
	}
}
