package ioa

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func TestFailedInstallCannotRemoveAnotherModulesCommands(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer srv.Close()
	r := commands.NewRegistry()
	defer r.Close(context.Background())
	config := Config{URL: srv.URL, RegisterCommands: true}
	first := New(config, r, nil)
	if err := first.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer first.Close(context.Background())
	second := New(config, r, nil)
	if err := second.Start(t.Context()); !errors.Is(err, commands.ErrDuplicateCommand) {
		t.Fatalf("conflicting Load: %v", err)
	}
	if err := second.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !r.Has("ioa") {
		t.Fatal("failed install removed the original owner's command")
	}
	if err := second.Start(t.Context()); err == nil {
		t.Fatal("reused failed instance")
	}
	if err := first.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.Has("ioa") {
		t.Fatal("successful owner did not revoke its registration")
	}
	if calls.Start() != 0 {
		t.Fatal("command construction performed network registration")
	}
}

func TestIOACommandSelectionRequiresRegistry(t *testing.T) {
	m := New(Config{RegisterCommands: true}, nil, nil)
	if err := m.Start(t.Context()); err == nil {
		t.Fatal("silently ignored requested command registration")
	}
	if err := m.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrationRetryOutlivesLoadAndStopsWithModule(t *testing.T) {
	var calls atomic.Int32
	retrying, canceled := make(chan struct{}), make(chan struct{})
	stopServer := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			http.Error(w, "try later", http.StatusServiceUnavailable)
			return
		}
		close(retrying)
		select {
		case <-r.Context().Done():
			close(canceled)
		case <-stopServer:
		}
	}))
	defer srv.Close()
	defer close(stopServer)

	caller, cancelCaller := context.WithCancel(context.Background())
	instance := New(Config{URL: srv.URL, AutoRegister: true}, commands.NewRegistry(), telemetry.NopLogger())
	if err := instance.Start(caller); err != nil {
		t.Fatal(err)
	}
	cancelCaller()
	select {
	case <-retrying:
	case <-time.After(5 * time.Second):
		t.Fatal("load context canceled registration retries")
	}
	if err := instance.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("instance close did not cancel the in-flight registration")
	}
	if err := instance.Start(context.Background()); err == nil {
		t.Fatal("closed IOA instance started again")
	}
}
