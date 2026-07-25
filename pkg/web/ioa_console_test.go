package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chainreactors/ioa/protocols"
	ioaserver "github.com/chainreactors/ioa/server"
)

var _ IOAConsoleReader = (*ioaserver.Service)(nil)

type fakeIOAConsole struct{}

func (fakeIOAConsole) ListNodes(context.Context) ([]protocols.Node, error) {
	return []protocols.Node{{ID: "node-1", Name: "scanner-1"}}, nil
}

func (fakeIOAConsole) ListSpaces(context.Context) ([]protocols.SpaceInfo, error) {
	return []protocols.SpaceInfo{{ID: "space-1", Name: "default", MessageCount: 1}}, nil
}

func (fakeIOAConsole) ListMessages(context.Context, protocols.MessageFilter) ([]protocols.Message, error) {
	return []protocols.Message{{
		ID: "message-1", SpaceID: "space-1", Sender: "node-1",
		Content: map[string]any{"content": "hello"},
	}}, nil
}

func TestIOAOverview(t *testing.T) {
	svc := NewService(ServiceConfig{})
	server := httptest.NewServer(NewHandler(svc, nil, nil, nil, nil, "", fakeIOAConsole{}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/ioa/overview")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var overview ioaOverviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&overview); err != nil {
		t.Fatal(err)
	}
	if len(overview.Nodes) != 1 || overview.Nodes[0].ID != "node-1" {
		t.Fatalf("nodes = %+v", overview.Nodes)
	}
	if len(overview.Spaces) != 1 || overview.Spaces[0].ID != "space-1" {
		t.Fatalf("spaces = %+v", overview.Spaces)
	}
	if len(overview.Messages) != 1 || overview.Messages[0].ID != "message-1" {
		t.Fatalf("messages = %+v", overview.Messages)
	}
}
