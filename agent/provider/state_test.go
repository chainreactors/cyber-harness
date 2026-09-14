package provider

import (
	"context"
	"github.com/chainreactors/aiscan/core/telemetry"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type borrowedStateProvider struct{ closed bool }

func (*borrowedStateProvider) Name() string { return "borrowed" }
func (*borrowedStateProvider) ChatCompletion(context.Context, *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	return &ChatCompletionResponse{}, nil
}
func (p *borrowedStateProvider) CloseIdleConnections() { p.closed = true }
func TestStateClosesOwnedTransportsAndPreservesBorrowedProvider(t *testing.T) {
	closed := make(chan struct{}, 4)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed <- struct{}{}
		}
	}
	server.Start()
	defer server.Close()
	var state State
	defer state.reset()
	p, _, err := state.Reload(t.Context(), ProviderConfig{Provider: "openai", Model: "fixture", APIKey: "fixture", BaseURL: server.URL + "/v1"}, telemetry.NopLogger())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ChatCompletion(t.Context(), &ChatCompletionRequest{}); err != nil {
		t.Fatal(err)
	}
	borrowed := &borrowedStateProvider{}
	state.Set(borrowed, ProviderConfig{Model: "borrowed"})
	state.reset()
	for range 2 {
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Fatal("probe or retained provider transport did not close")
		}
	}
	if borrowed.closed {
		t.Fatal("closed a borrowed provider")
	}
}
