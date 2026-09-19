package ioa

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestReaderLifetime(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(entered)
		<-r.Context().Done()
		close(canceled)
	}))
	defer server.Close()
	resource := New(Config{URL: server.URL}, nil)
	defer resource.Close(context.Background())
	reader := resource.Service
	if _, err := reader.ListSpaces(t.Context()); err == nil {
		t.Fatal("query before Load succeeded")
	}
	if err := resource.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := reader.ListSpaces(context.Background()); result <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("query did not reach server")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := resource.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("in-flight query error = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Close left an in-flight query")
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("Close did not cancel the HTTP request")
	}
	if _, err := reader.ListNodes(t.Context()); err == nil {
		t.Fatal("retained reader accepted query after Close")
	}
	if calls.Load() != 1 {
		t.Fatalf("requests outside lifetime: %d", calls.Load())
	}
}
