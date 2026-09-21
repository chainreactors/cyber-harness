package ioa

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/core/telemetry"
)

func TestServiceReturnsCommandDeclarationsWithoutOwningARegistry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer srv.Close()
	config := Config{URL: srv.URL, RegisterCommands: true}
	service := New(config, nil)
	if err := service.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(service.Service.Commands()) == 0 {
		t.Fatal("started service did not expose command declarations")
	}
	if err := service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(service.Service.Commands()) != 0 {
		t.Fatal("closed service retained command declarations")
	}
	if calls.Load() != 0 {
		t.Fatal("command construction performed network registration")
	}
}

func TestServiceWithoutURLUsesMemory(t *testing.T) {
	m := New(Config{RegisterCommands: true}, nil)
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(m.Service.Commands()) == 0 {
		t.Fatal("memory service did not expose commands")
	}
	if err := m.Service.WaitReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !m.Service.Status().Bound || m.Service.ReceiveSpace() == "" {
		t.Fatal("memory service is not ready")
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
	instance := New(Config{URL: srv.URL, AutoRegister: true}, telemetry.NopLogger())
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
