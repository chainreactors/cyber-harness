package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubscriptionRefreshRetainsRouteAndFailureSnapshot(t *testing.T) {
	var mode atomic.Int32
	requests := make(chan struct{}, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			select {
			case requests <- struct{}{}:
			default:
			}
		}()
		if r.UserAgent() != clashSubscriptionUA {
			t.Errorf("unexpected UA: %s", r.UserAgent())
		}
		switch mode.Load() {
		case 1:
			w.WriteHeader(http.StatusServiceUnavailable)
		case 2:
			fmt.Fprint(w, "socks5://127.0.0.1:1001#one\nsocks5://127.0.0.1:1002#two\n")
		default:
			fmt.Fprint(w, "socks5://127.0.0.1:1001#one\n")
		}
	}))
	defer server.Close()
	state := NewState("")
	sub, err := NewSubscription(t.Context(), t.Context(), state, SubscriptionConfig{URL: server.URL + "/private-token", Strategy: "first", Refresh: "15ms"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sub.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if !state.IsAutoMode() || strings.Contains(state.ActiveProxy(), "private-token") {
		t.Fatalf("unsafe or inactive configured proxy: %s", state.ActiveProxy())
	}
	if err := state.Switch("one"); err != nil {
		t.Fatal(err)
	}
	route := state.ActiveProxy()
	mode.Store(1)
	deadline := time.After(2 * time.Second)
	for {
		_, refreshErr := sub.Status()
		if refreshErr != nil {
			break
		}
		select {
		case <-requests:
		case <-deadline:
			t.Fatal("refresh failure was not reported")
		}
	}
	if len(state.Nodes()) != 1 || state.ActiveProxy() != route || state.IsAutoMode() {
		t.Fatal("failed refresh replaced the snapshot or route")
	}
	mode.Store(2)
	for len(state.Nodes()) != 2 {
		select {
		case <-requests:
		case <-deadline:
			t.Fatal("refresh did not recover")
		}
	}
	if state.ActiveProxy() != route || state.IsAutoMode() {
		t.Fatal("successful refresh changed the user's selection")
	}
	state.Clear()
	for {
		_, refreshErr := sub.Status()
		if refreshErr == errSubscriptionReplaced {
			break
		}
		select {
		case <-requests:
		case <-sub.done:
			goto stopped
		case <-deadline:
			t.Fatal("old refresh did not stop")
		}
	}
stopped:
	if len(state.Nodes()) != 0 || state.ActiveProxy() != "" {
		t.Fatal("cleared subscription was resurrected")
	}
}

func TestSubscriptionCloseCancelsInFlightRefresh(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fmt.Fprint(w, "socks5://127.0.0.1:1001#one\n")
			return
		}
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	sub, err := NewSubscription(t.Context(), t.Context(), NewState(""), SubscriptionConfig{URL: server.URL + "/credential", Refresh: "1ms"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := sub.Close(ctx); err != nil {
		t.Fatalf("refresh did not drain: %v", err)
	}
}

func TestSubscriptionFailureDoesNotPublish(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	state := NewState("socks5://127.0.0.1:1001")
	_, err := NewSubscription(t.Context(), t.Context(), state, SubscriptionConfig{URL: server.URL + "/token-secret"})
	if err == nil || strings.Contains(err.Error(), "token-secret") {
		t.Fatalf("unsafe failure: %v", err)
	}
	if state.ActiveProxy() != "socks5://127.0.0.1:1001" {
		t.Fatal("failed fetch changed routing")
	}
}
