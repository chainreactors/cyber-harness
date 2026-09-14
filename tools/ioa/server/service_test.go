package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ioaclient "github.com/chainreactors/ioa/client"
)

func TestServerConstructionIsInertAndHandlerIsGated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ioa.sqlite")
	resource := New(Config{Database: path})
	handler := resource.Server.Handler()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("constructor opened store: %v", err)
	}
	check := func(want int) {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "/health", nil))
		if response.Code != want {
			t.Fatalf("status = %d, want %d", response.Code, want)
		}
	}
	check(503)
	if err := resource.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(200)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/mcp", nil))
	if response.Code != 404 {
		t.Fatalf("unselected MCP route = %d", response.Code)
	}
	if err := resource.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(503)
	if _, err := resource.store.ListSpaces(); err == nil {
		t.Fatal("store remained open")
	}
	if err := resource.Start(t.Context()); err == nil {
		t.Fatal("closed server restarted")
	}
	if err := resource.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCloseCancelsAndJoinsRequestsBeforeClosingStore(t *testing.T) {
	resource := New(Config{})
	if err := resource.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	entered, canceled, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	resource.Server.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(canceled)
		<-release
	})
	go func() {
		defer close(returned)
		resource.Server.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/blocked", nil))
	}()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := resource.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close = %v", err)
	}
	<-canceled
	if _, err := resource.store.ListSpaces(); err != nil {
		t.Fatalf("store closed before request returned: %v", err)
	}
	close(release)
	<-returned
	if err := resource.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := resource.store.ListSpaces(); err == nil {
		t.Fatal("retry did not release store")
	}
}

func TestServerClosesSSEAndPreservesMCP(t *testing.T) {
	resource := New(Config{AccessKey: "test-key", MCP: true})
	if err := resource.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer resource.Close(context.Background())
	server := httptest.NewServer(resource.Server.Handler())
	defer server.Close()
	client, err := ioaclient.NewClient(strings.Replace(server.URL, "http://", "http://test-key@", 1), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureRegistered(t.Context(), "receiver", "", nil); err != nil {
		t.Fatal(err)
	}
	space, err := client.Space(t.Context(), "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	_, streamErrors, stop, err := client.Subscribe(t.Context(), space.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	request := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response := httptest.NewRecorder()
	resource.Server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "protocolVersion") {
		t.Fatalf("MCP = %d %s", response.Code, response.Body.String())
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := resource.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-streamErrors:
	case <-ctx.Done():
		t.Fatal("SSE did not exit")
	}
	result, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	_, _ = io.Copy(io.Discard, result.Body)
	if result.StatusCode != 503 {
		t.Fatalf("closed status = %d", result.StatusCode)
	}
}
