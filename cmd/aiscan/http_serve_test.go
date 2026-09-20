package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/chainreactors/cyber/core/extension"
	ioaserver "github.com/chainreactors/cyber/pkg/exts/ioa/server"
	ioaservice "github.com/chainreactors/cyber/tools/ioa/server"
	ioaclient "github.com/chainreactors/ioa/client"
)

func TestManagedHTTPShutdownCancelsSSEBeforeDraining(t *testing.T) {
	owner := ioaserver.New(ioaservice.Config{AccessKey: "test-key"})
	set, err := extension.New(owner)
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
