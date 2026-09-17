package console

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	aop "github.com/chainreactors/cyber/aop"
	cfg "github.com/chainreactors/cyber/core/config"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	rlterm "github.com/chainreactors/tui/readline/terminal"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type gateProvider struct {
	release chan struct{}
	calls   atomic.Int32
	mu      sync.Mutex
	inputs  []string
}

func (*gateProvider) Name() string { return "gate" }
func (p *gateProvider) ChatCompletion(ctx context.Context, req *agent.ChatCompletionRequest) (*agent.ChatCompletionResponse, error) {
	call := p.calls.Add(1)
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			p.mu.Lock()
			p.inputs = append(p.inputs, provider.MessageText(req.Messages[i]))
			p.mu.Unlock()
			break
		}
	}
	if call == 1 {
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &agent.ChatCompletionResponse{Choices: []agent.Choice{{Message: agent.TextMessage("assistant", "done")}}}, nil
}
func newTestConsole(t *testing.T, option *cfg.Option, provider agent.Provider, stdout, stderr io.Writer) (*AgentConsole, *apppkg.App) {
	t.Helper()
	if option == nil {
		option = &cfg.Option{}
	}
	rt, application := newConsoleRuntime(t, provider)
	session, err := rt.OpenSession(context.Background(), agentsession.SessionOptions{ID: "console-test"})
	if err != nil {
		t.Fatal(err)
	}
	c := newAgentConsole(context.Background(), rt, session, option, rlterm.Stream(strings.NewReader(""), stdout, stderr, rlterm.NewControl(false, 80, 24)), testSessionBindings(t, rt))
	t.Cleanup(c.Close)
	return c, application
}
func executeAndWait(r *AgentConsole, line string) (bool, error) {
	done, err := r.handleInputLine(line)
	r.work.Wait()
	return done, err
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
func TestConsoleSubmissionsUseRuntimeFIFOAndLimit(t *testing.T) {
	p := &gateProvider{release: make(chan struct{})}
	c, _ := newTestConsole(t, &cfg.Option{}, p, io.Discard, io.Discard)
	if err := c.submitPrompt("first", false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.calls.Load() == 1 }, "provider")
	for i := 1; i < agentsession.DefaultSessionPendingLimit; i++ {
		if err := c.submitPrompt(fmt.Sprintf("queued-%02d", i), false); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.submitPrompt("overflow", false); err == nil {
		t.Fatal("bypassed Runtime limit")
	}
	close(p.release)
	c.work.Wait()
	if p.calls.Load() != agentsession.DefaultSessionPendingLimit {
		t.Fatalf("calls=%d", p.calls.Load())
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inputs[0] != "first" || p.inputs[1] != "queued-01" {
		t.Fatalf("inputs=%v", p.inputs)
	}
	for i := 1; i < len(p.inputs); i++ {
		if p.inputs[i] != fmt.Sprintf("queued-%02d", i) {
			t.Fatalf("FIFO broken at %d: %q", i, p.inputs[i])
		}
	}
	c.workMu.Lock()
	defer c.workMu.Unlock()
	if len(c.previews) != 0 {
		t.Fatalf("stale previews=%v", c.previews)
	}
}
func TestConsoleStopCancelsOnlyItsOwnSubmissions(t *testing.T) {
	p := &gateProvider{release: make(chan struct{})}
	c, _ := newTestConsole(t, &cfg.Option{}, p, io.Discard, io.Discard)
	if err := c.submitPrompt("first", false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.calls.Load() == 1 }, "provider")
	if err := c.submitPrompt("must cancel", false); err != nil {
		t.Fatal(err)
	}
	other, err := c.session.Run(context.Background(), agentsession.RunInput{Content: []*aop.Content{aop.Text("inline")}})
	if err != nil {
		t.Fatal(err)
	}
	if !c.InterruptCurrentRun() {
		t.Fatal("stop did not cancel")
	}
	c.work.Wait()
	if _, err = other.Wait(); err != nil {
		t.Fatal(err)
	}
	if err = c.submitPrompt("new batch", false); err != nil {
		t.Fatal(err)
	}
	c.work.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, text := range p.inputs {
		if text == "must cancel" {
			t.Fatal("canceled work executed")
		}
	}
	if len(p.inputs) != 3 {
		t.Fatalf("inputs=%v", p.inputs)
	}
}
func TestConsoleCloseRejectsInputAndLeavesProfileSession(t *testing.T) {
	c, _ := newTestConsole(t, &cfg.Option{}, &consoleProvider{}, io.Discard, io.Discard)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = c.submitPrompt("racing", false) }()
	}
	c.Close()
	wg.Wait()
	c.Close()
	if err := c.submitPrompt("late", false); err == nil {
		t.Fatal("accepted after close")
	}
	if _, err := c.session.Command(context.Background(), "/status"); err != nil {
		t.Fatal(err)
	}
}
