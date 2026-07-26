package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

// stdioGateProvider blocks every call until the gate closes, recording the
// user prompt of each call in start order.
type stdioGateProvider struct {
	gate chan struct{}

	mu      sync.Mutex
	prompts []string
}

func newStdioGateProvider() *stdioGateProvider {
	return &stdioGateProvider{gate: make(chan struct{})}
}

func (p *stdioGateProvider) Name() string { return "stdio-gate" }

func (p *stdioGateProvider) ChatCompletion(ctx context.Context, req *agent.ChatCompletionRequest) (*agent.ChatCompletionResponse, error) {
	p.mu.Lock()
	p.prompts = append(p.prompts, lastUserText(req.Messages))
	p.mu.Unlock()
	select {
	case <-p.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &agent.ChatCompletionResponse{
		Choices: []agent.Choice{{Message: agent.NewTextMessage("assistant", "done")}},
	}, nil
}

func (p *stdioGateProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.prompts)
}

func (p *stdioGateProvider) promptsSnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.prompts...)
}

func lastUserText(messages []agent.ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && messages[i].Content != nil {
			return *messages[i].Content
		}
	}
	return ""
}

func newStdioTestSession(t *testing.T, h *stdioHost, output *bytes.Buffer, id string, prov agent.Provider) {
	t.Helper()
	if h.rt == nil || h.rt.ctx == nil {
		initialized := newRuntimeStdioHost(t, output, prov)
		h.rt = initialized.rt
	}
	h.accept(openSessionLine(t, id))
}

func newRuntimeStdioHost(t *testing.T, output *bytes.Buffer, prov agent.Provider) *stdioHost {
	t.Helper()
	h := newStdioHost(context.Background(), nil, nil, output)
	h.rt = newBareRuntime(t, nil, prov)
	h.rt.config.Model = "test"
	h.rt.config.MaxTurns = 4
	h.rt.Subscribe(func(event aop.Event) {
		payload, _ := json.Marshal(event)
		_ = h.emit(webproto.Message{Type: webproto.TypeAOP, TurnID: event.TurnID, Payload: payload})
	})
	return h
}

func waitForCalls(t *testing.T, prov *stdioGateProvider, n int, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if prov.callCount() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (calls = %d, want %d)", what, prov.callCount(), n)
}

func TestStdioSameSessionFIFOOrder(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)
	prov := newStdioGateProvider()
	newStdioTestSession(t, h, &output, "s1", prov)
	defer h.rt.Close()

	for _, text := range []string{"first", "second", "third"} {
		h.accept(runLine(t, "s1", "turn-"+text, text))
	}
	waitForCalls(t, prov, 1, "first run to start")
	close(prov.gate)
	h.drain()

	prompts := prov.promptsSnapshot()
	if len(prompts) != 3 || prompts[0] != "first" || prompts[1] != "second" || prompts[2] != "third" {
		t.Fatalf("prompt order = %v, want [first second third]", prompts)
	}
}

func TestStdioSessionsRunConcurrently(t *testing.T) {
	var output bytes.Buffer
	prov := newStdioGateProvider()
	h := newRuntimeStdioHost(t, &output, prov)
	defer h.rt.Close()

	h.accept(openSessionLine(t, "s1"))
	h.accept(openSessionLine(t, "s2"))
	h.accept(runLine(t, "s1", "turn-one", "one"))
	h.accept(runLine(t, "s2", "turn-two", "two"))

	// Both sessions are mid-run at the same time: neither FIFO blocks the other.
	waitForCalls(t, prov, 2, "both session runs to start")

	close(prov.gate)
	h.drain()
	h.accept(protocolLine(t, webproto.Message{Type: webproto.TypeSessionClose, Payload: mustJSON(t, webproto.SessionLifecyclePayload{SessionID: "s1", Reason: "completed"})}))
	h.accept(protocolLine(t, webproto.Message{Type: webproto.TypeSessionClose, Payload: mustJSON(t, webproto.SessionLifecyclePayload{SessionID: "s2", Reason: "completed"})}))

	// Interleaved output must stay valid AOP: every line decodes, and both
	// sessions produced their session brackets.
	events := decodeAOPMessages(t, decodeProtocolLines(t, &output))
	starts := map[string]bool{}
	ends := map[string]bool{}
	for _, e := range events {
		if e.SessionID != "s1" && e.SessionID != "s2" {
			t.Fatalf("event with foreign session: %+v", e)
		}
		switch e.Type {
		case aop.TypeSessionStart:
			starts[e.SessionID] = true
		case aop.TypeSessionEnd:
			ends[e.SessionID] = true
		}
	}
	if !starts["s1"] || !starts["s2"] || !ends["s1"] || !ends["s2"] {
		t.Fatalf("missing session brackets: starts=%v ends=%v", starts, ends)
	}
}

func TestStdioDrainWaitsForInFlightAndQueued(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)
	prov := newStdioGateProvider()
	newStdioTestSession(t, h, &output, "s1", prov)
	defer h.rt.Close()

	h.accept(runLine(t, "s1", "turn-first", "first"))
	h.accept(runLine(t, "s1", "turn-second", "second"))
	waitForCalls(t, prov, 1, "first run to start")

	drained := make(chan struct{})
	go func() {
		h.drain()
		close(drained)
	}()

	select {
	case <-drained:
		t.Fatal("drain returned while a run was in flight")
	case <-time.After(100 * time.Millisecond):
	}

	close(prov.gate)
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not return after runs completed")
	}
	if got := prov.callCount(); got != 2 {
		t.Fatalf("calls = %d, want 2 (queued message must run before drain returns)", got)
	}
}
