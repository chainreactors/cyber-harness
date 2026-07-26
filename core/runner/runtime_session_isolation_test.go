package runner

import (
	"context"
	"fmt"
	"testing"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

func newBareRuntime(t *testing.T, reg *commands.CommandRegistry, provider agent.Provider) *AgentRuntime {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	if reg == nil {
		reg = commands.NewRegistry()
	}
	publicBus := eventbus.New[aop.Event]()
	kernelBus := eventbus.New[aop.Event]()
	events := newSessionEmitter(publicBus)
	kernelBus.Subscribe(events.emit)
	rt := &AgentRuntime{
		app: &App{Commands: reg}, option: &cfg.Option{}, ctx: ctx, cancel: cancel,
		sessions: make(map[string]*sessionState), runs: make(map[string]*Run),
		bus: publicBus, kernelBus: kernelBus, sessionEvents: events,
		config: agent.Config{Provider: provider, Tools: reg, Bus: kernelBus, Logger: telemetry.NopLogger()},
	}
	t.Cleanup(rt.Close)
	return rt
}

func TestRuntimeSessionDirectLoopUsesSessionScheduler(t *testing.T) {
	reg := commands.NewRegistry()
	commands.BuildGroup("core", &commands.Deps{WorkDir: t.TempDir(), BashTimeout: 5, Logger: telemetry.NopLogger()}, reg)
	loop := agent.NewLoopCommand()
	reg.Register(commands.Command{Name: loop.Name(), Usage: loop.Usage(), Run: loop.Run}, "loop")
	rt := newBareRuntime(t, reg, nil)
	t.Cleanup(func() {
		for _, tool := range reg.Tools() {
			if closer, ok := tool.(interface{ Close() }); ok {
				closer.Close()
			}
		}
	})

	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "chat-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Command(context.Background(), "!loop 10s check progress"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for session.state.scheduler.Active() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := session.state.scheduler.Active(); got != 1 {
		t.Fatalf("session scheduler active = %d, want 1", got)
	}
}

func TestRuntimeSessionRejectsRequestsPastPendingLimit(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	session, err := rt.OpenSession(context.Background(), SessionOptions{ID: "chat-1"})
	if err != nil {
		t.Fatal(err)
	}
	block := func(ctx context.Context) { <-ctx.Done() }
	for i := 0; i < DefaultSessionPendingLimit; i++ {
		op := &sessionOperation{
			execute: block,
			reject:  func(error) {},
		}
		if err := session.state.admit(context.Background(), op); err != nil {
			t.Fatalf("admit request %d: %v", i, err)
		}
	}
	op := &sessionOperation{execute: block, reject: func(error) {}}
	if err := session.state.admit(context.Background(), op); err == nil {
		t.Fatal("request past pending limit was admitted")
	} else if got := err.Error(); got == "" {
		t.Fatal(fmt.Errorf("empty overflow error"))
	}
}
