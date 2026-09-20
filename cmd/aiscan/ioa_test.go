package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	ioaserver "github.com/chainreactors/cyber/pkg/exts/ioa/server"
	ioatools "github.com/chainreactors/cyber/tools/ioa"
	ioaservice "github.com/chainreactors/cyber/tools/ioa/server"
	ioaclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/ioa/protocols"
)

func TestProfileWithoutIOAHasNoCollaborationContributions(t *testing.T) {
	p, err := buildAIScanProfile(minimalConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	if err := p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	registry, _ := p.Shell()
	if registry.Has("ioa") {
		t.Fatal("unselected client contributed commands or skills")
	}
	if p.ConsoleBindings() != nil || p.AgentStatus().Bound {
		t.Fatal("unselected client exposed host capabilities")
	}
}

func TestServerOutlivesClientProfileReplacement(t *testing.T) {
	serverExtension := ioaserver.New(ioaservice.Config{AccessKey: "test-key"})
	host, err := extension.New(serverExtension)
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
		p, err := buildAIScanProfile(config)
		if err != nil {
			t.Fatal(err)
		}
		if p.AgentStatus().Bound || p.ConsoleBindings() != nil {
			t.Fatal("unpublished profile exposed IOA state")
		}
		if err := p.Load(t.Context()); err != nil {
			t.Fatal(err)
		}
		registry, _ := p.Shell()
		if !registry.Has("ioa") {
			t.Fatal("client contribution is incomplete")
		}
		if !p.AgentStatus().Bound {
			t.Fatal("bound client status missing")
		}
		reader := p.ioa
		if reader == nil {
			t.Fatal("loaded profile did not publish its query capability")
		}
		spaces, err := reader.ListSpaces(t.Context())
		if err != nil || len(spaces) != 1 || spaces[0].ID != space.ID {
			t.Fatalf("profile queries = %v, %v", spaces, err)
		}
		if err := p.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if p.AgentStatus().Bound || p.ConsoleBindings() != nil {
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
