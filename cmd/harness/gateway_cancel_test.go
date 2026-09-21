//go:build live_llm

package harness_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGatewayCallerCancellationPreservesEvidence(t *testing.T) {
	started := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[]}\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer upstream.Close()
	log, err := os.Create(filepath.Join(t.TempDir(), "model.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	g := &subagentGateway{config: map[string]any{"baseUrl": upstream.URL, "apiKey": "local-test"}, log: log, longTask: true, failure: make(chan error, 1)}
	served := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(served)
		g.serve(t.Context(), w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/chat/completions", strings.NewReader(`{"model":"test","messages":[{"role":"user","content":"ping"}],"stream":true,"max_tokens":16}`))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, _ := http.DefaultClient.Do(req)
		if response != nil {
			response.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream did not start")
	}
	cancel()
	select {
	case <-served:
	case <-time.After(3 * time.Second):
		t.Fatal("gateway did not release canceled request")
	}
	<-done
	select {
	case err := <-g.failure:
		t.Fatalf("caller cancellation rejected: %v", err)
	default:
	}
	if g.outputTokens != 16384 || g.usageMissing != 1 {
		t.Fatalf("missing usage unaccounted: %d, %d", g.outputTokens, g.usageMissing)
	}
	raw, err := os.ReadFile(log.Name())
	if err != nil || !strings.Contains(string(raw), `"canceled_request":1`) {
		t.Fatalf("missing cancellation evidence: %s %v", raw, err)
	}
}
