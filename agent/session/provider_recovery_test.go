package session

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
)

type recoveryObserver func(*aop.Event)

func (f recoveryObserver) ObserveEvent(event *aop.Event) { f(event) }

func writeRecoveryStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
}

func TestProviderFailuresAllowNextTurnInSameSession(t *testing.T) {
	for _, failure := range []string{"quota", "connection-reset", "stalled-stream"} {
		t.Run(failure, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) > 1 {
					writeRecoveryStream(w)
					return
				}
				switch failure {
				case "quota":
					http.Error(w, `{"error":{"message":"insufficient quota"}}`, http.StatusPaymentRequired)
				case "connection-reset":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						_ = conn.Close()
					}
				case "stalled-stream":
					w.Header().Set("Content-Type", "text/event-stream")
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			p, err := provider.NewProvider(&provider.ProviderConfig{Provider: "openai", BaseURL: server.URL + "/v1", APIKey: "fixture", Model: "fixture", Timeout: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer p.(interface{ CloseIdleConnections() }).CloseIdleConnections()
			rt := newBareRuntime(t, nil, p)
			rt.config.Providers.Set(p, provider.ProviderConfig{Model: "fixture"})
			rt.agentConfig.MaxRetries = -1
			conversation, err := rt.OpenSession(t.Context(), SessionOptions{ID: failure, Messages: []*aop.Message{provider.TextMessage("user", "remember my HAR")}})
			if err != nil {
				t.Fatal(err)
			}
			terminals := make(chan *aop.Event, 2)
			subscription := rt.Observe(recoveryObserver(func(event *aop.Event) {
				if event.GetTurnEnded() != nil {
					terminals <- event
				}
			}))
			defer subscription.Cancel()
			failed, err := conversation.Run(t.Context(), RunInput{TurnID: "failed", Message: agent.TextInput("first attempt")})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-failed.done:
			case <-time.After(3 * time.Second):
				t.Fatal("failed provider stranded the run")
			}
			if result, err := failed.Wait(); err == nil || result.Stop != agenthooks.StopReasonError {
				t.Fatalf("failed run: %v %v", result, err)
			}
			terminal := <-terminals
			if terminal.TurnId != "failed" || terminal.GetTurnEnded().Error == nil {
				t.Fatalf("failure terminal = %v", terminal)
			}
			next, err := conversation.Run(t.Context(), RunInput{TurnID: "recovered", Message: agent.TextInput("continue in the same session")})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-next.done:
			case <-time.After(time.Second):
				t.Fatal("next input could not run after failure")
			}
			result, err := next.Wait()
			if err != nil || !strings.Contains(result.Output, "recovered") || provider.MessageText(conversation.MessagesSnapshot()[0]) != "remember my HAR" {
				t.Fatalf("session did not recover with its context: %v %v", result, err)
			}
		})
	}
}

func TestCancelStalledProviderDrainsBeforeQueuedTurn(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 1 {
			writeRecoveryStream(w)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	p, err := provider.NewProvider(&provider.ProviderConfig{Provider: "openai", BaseURL: server.URL + "/v1", APIKey: "fixture", Model: "fixture", Timeout: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer p.(interface{ CloseIdleConnections() }).CloseIdleConnections()
	rt := newBareRuntime(t, nil, p)
	rt.config.Providers.Set(p, provider.ProviderConfig{Model: "fixture"})
	conversation, err := rt.EnsureSession(SessionOptions{ID: "paused"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := conversation.Run(t.Context(), RunInput{TurnID: "first", Message: agent.TextInput("wait")})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}
	queued, err := conversation.Run(t.Context(), RunInput{TurnID: "queued", Message: agent.TextInput("new instruction")})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.CancelSessionRun("paused", "first"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.done:
	case <-time.After(time.Second):
		t.Fatal("pause did not interrupt the stalled HTTP stream")
	}
	if _, err := first.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("pause result = %v", err)
	}
	select {
	case <-queued.done:
	case <-time.After(time.Second):
		t.Fatal("queued instruction stayed blocked after pause")
	}
	if result, err := queued.Wait(); err != nil || !strings.Contains(result.Output, "recovered") {
		t.Fatalf("queued result = %v, %v", result, err)
	}
}
