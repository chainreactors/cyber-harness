package web

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	chatpb "github.com/chainreactors/aiscan/aop/aiscan/chat"
	"github.com/chainreactors/aiscan/aop/aiscan/chat/chatconnect"
)

func TestParseCommand(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantCmd string
		wantArg string
		wantOK  bool
	}{
		{"scan with target", "/scan example.com", "scan", "example.com", true},
		{"scan with flags", "/scan example.com --mode full --deep", "scan", "example.com --mode full --deep", true},
		{"verb only", "/agents", "agents", "", true},
		{"lowercased verb", "/SCAN Example.com", "scan", "Example.com", true},
		{"extra spaces", "/scan    a.com   b.com", "scan", "a.com   b.com", true},
		{"tab separator", "/help\tx", "help", "x", true},
		{"plain message", "hello there", "", "", false},
		{"bare slash", "/", "", "", false},
		{"slash then spaces", "/   ", "", "", false},
		{"path-like, not a command", "/etc/passwd", "etc/passwd", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, arg, ok := parseCommand(tc.in)
			if ok != tc.wantOK || cmd != tc.wantCmd || arg != tc.wantArg {
				t.Fatalf("parseCommand(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.in, cmd, arg, ok, tc.wantCmd, tc.wantArg, tc.wantOK)
			}
		})
	}
}

func newMenuTestService(t *testing.T) *Service {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewService(ServiceConfig{Store: store})
}

// TestSessionMenuMergeAndFallback checks the "/" menu the hub serves: hub-scope
// commands merged with the agent's (here, the static fallback since no agent is
// bound), with run-control commands excluded.
func TestSessionMenuMergeAndFallback(t *testing.T) {
	svc := newMenuTestService(t)
	names := map[string]bool{}
	for _, s := range svc.SessionMenu("no-such-session") {
		names[s.Name] = true
	}
	for _, want := range []string{"/agents", "/help", "/status", "/provider", "/model"} {
		if !names[want] {
			t.Errorf("SessionMenu missing %q", want)
		}
	}
	for _, absent := range []string{"/scan", "/stop", "/continue", "/eval", "/followup", "/loop"} {
		if names[absent] {
			t.Errorf("SessionMenu leaked run-control command %q", absent)
		}
	}
}

// TestSessionCommandsConnectRPC drives the generated Connect endpoint the
// frontend "/" menu uses and proves it returns the protobuf command catalog.
func TestSessionCommandsConnectRPC(t *testing.T) {
	svc := newMenuTestService(t)
	srv := httptest.NewServer(NewHandler(svc, nil, nil, nil, nil, ""))
	defer srv.Close()

	client := chatconnect.NewSessionServiceClient(srv.Client(), srv.URL, connect.WithProtoJSON())
	resp, err := client.ListCommands(context.Background(), connect.NewRequest(&chatpb.ListCommandsRequest{SessionId: "anything"}))
	if err != nil {
		t.Fatalf("ListCommands: %v", err)
	}
	names := map[string]bool{}
	for _, s := range resp.Msg.Commands {
		names[s.Name] = true
	}
	for _, want := range []string{"/help", "/status", "/model"} {
		if !names[want] {
			t.Errorf("ListCommands response missing %q (got %d specs)", want, len(resp.Msg.Commands))
		}
	}
	if names["/scan"] {
		t.Error("ListCommands leaked deferred scan command")
	}
}
