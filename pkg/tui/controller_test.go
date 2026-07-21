package tui

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent"
)

// gateProvider blocks the first call until released; later calls answer
// immediately. Used to keep a run in flight while input is queued.
type gateProvider struct {
	release  chan struct{}
	released atomic.Bool
	calls    atomic.Int32
}

func (p *gateProvider) Name() string { return "gate" }

func (p *gateProvider) ChatCompletion(ctx context.Context, _ *agent.ChatCompletionRequest) (*agent.ChatCompletionResponse, error) {
	call := p.calls.Add(1)
	if call == 1 && !p.released.Load() {
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &agent.ChatCompletionResponse{
		Choices: []agent.Choice{{Message: agent.NewTextMessage("assistant", "done")}},
	}, nil
}

func newTestController(t *testing.T, prov agent.Provider) *interactiveRunController {
	t.Helper()
	ag := agent.NewAgent(agent.Config{
		Provider: prov,
		Model:    "test-model",
	})
	out := NewAgentOutputWithWriters(nil, io.Discard, io.Discard, false)
	return newInteractiveRunController(context.Background(), ag, out)
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

func TestSubmitPromptQueuesWhileRunning(t *testing.T) {
	prov := &gateProvider{release: make(chan struct{})}
	c := newTestController(t, prov)

	if err := c.SubmitPrompt("first", "first", "first"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return prov.calls.Load() == 1 }, "first run to reach provider")

	if err := c.SubmitPrompt("second", "second", "second"); err != nil {
		t.Fatal(err)
	}
	if err := c.SubmitPrompt("third", "third", "third"); err != nil {
		t.Fatal(err)
	}

	c.mu.Lock()
	queued := len(c.pending)
	c.mu.Unlock()
	if queued != 2 {
		t.Fatalf("pending = %d, want 2", queued)
	}

	close(prov.release)
	// Queued runs chain through drainPending; each performs one provider call.
	waitFor(t, func() bool { return prov.calls.Load() == 3 }, "queued runs to execute")
	waitFor(t, func() bool { return !c.Running() }, "controller to go idle")

	c.mu.Lock()
	left := len(c.pending)
	c.mu.Unlock()
	if left != 0 {
		t.Fatalf("pending after drain = %d, want 0", left)
	}
}

func TestQueuedRunsExecuteInFIFOOrder(t *testing.T) {
	prov := &gateProvider{release: make(chan struct{})}
	c := newTestController(t, prov)

	var mu sync.Mutex
	var order []string

	if err := c.SubmitPrompt("first", "first", "first"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return prov.calls.Load() == 1 }, "first run to reach provider")

	for _, label := range []string{"second", "third"} {
		label := label
		c.mu.Lock()
		c.pending = append(c.pending, pendingRun{
			label: label,
			run: func(ctx context.Context) (*agent.Result, error) {
				mu.Lock()
				order = append(order, label)
				mu.Unlock()
				return &agent.Result{Output: label}, nil
			},
		})
		c.mu.Unlock()
	}

	close(prov.release)
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) == 2
	}, "queued runs to execute")

	mu.Lock()
	defer mu.Unlock()
	if order[0] != "second" || order[1] != "third" {
		t.Fatalf("execution order = %v, want [second third]", order)
	}
}

func TestStopClearsPendingQueue(t *testing.T) {
	prov := &gateProvider{release: make(chan struct{})}
	c := newTestController(t, prov)

	if err := c.SubmitPrompt("first", "first", "first"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return prov.calls.Load() == 1 }, "run to reach provider")

	if err := c.SubmitPrompt("second", "second", "second"); err != nil {
		t.Fatal(err)
	}

	if !c.Stop() {
		t.Fatal("Stop() = false, want true")
	}
	c.Wait()
	close(prov.release)

	c.mu.Lock()
	left := len(c.pending)
	c.mu.Unlock()
	if left != 0 {
		t.Fatalf("pending after Stop = %d, want 0", left)
	}

	waitFor(t, func() bool { return !c.Running() }, "controller to go idle")
	if got := prov.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want 1 (queued run must not start)", got)
	}
}
