package commands

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent/tmux"
	"github.com/chainreactors/utils/pty"
)

func TestNestedExecutionReadsLiveSessionWithoutStateCopies(t *testing.T) {
	manager := tmux.NewManager()
	defer manager.Shutdown()
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	info, err := manager.CreateFunc(t.Context(), "local lifecycle", time.Minute, func(ctx context.Context, _ io.Writer) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	parent := NewExecution(manager, "parent", nil, t.TempDir(), nil)
	parent.BindID(info.ID)
	var child *Execution
	registry, _ := loadTestRegistry(t, commandBatch(Command{Name: "child", Run: func(_ context.Context, execution *Execution) (any, error) {
		child = execution
		return nil, nil
	}}))
	if _, err := registry.Run(t.Context(), []string{"child"}, parent); err != nil {
		t.Fatal(err)
	}
	for _, execution := range []*Execution{parent, child} {
		snapshot, ok := execution.Session()
		if !ok || snapshot.ID != info.ID || snapshot.State != pty.StateRunning {
			t.Fatalf("running session = %+v, retained = %v", snapshot, ok)
		}
	}
	unblock()
	select {
	case <-manager.Done(info.ID):
	case <-time.After(5 * time.Second):
		t.Fatal("local session did not finish")
	}
	// Neither invocation needs Wait or an explicit refresh to see completion.
	for _, execution := range []*Execution{parent, child} {
		snapshot, ok := execution.Session()
		if !ok || snapshot.State != pty.StateCompleted || snapshot.ExitCode != 0 || snapshot.EndedAt.IsZero() {
			t.Fatalf("completed session = %+v, retained = %v", snapshot, ok)
		}
	}
}

func TestInvocationIDDoesNotImplyTerminalSession(t *testing.T) {
	for _, execution := range []*Execution{nil, {}, {ID: "local-call"}} {
		if snapshot, ok := execution.Session(); ok || snapshot.ID != "" {
			t.Fatalf("invented terminal session: %+v, %v", snapshot, ok)
		}
	}
}

func TestCommandCorrelationWaitsForManagedSessionIdentity(t *testing.T) {
	execution := NewExecution(nil, "probe", nil, "", nil)
	result := make(chan string, 1)
	go func() {
		id, err := execution.waitID(t.Context())
		if err != nil {
			result <- "error: " + err.Error()
			return
		}
		result <- id
	}()
	select {
	case id := <-result:
		t.Fatalf("session identity returned before bind: %q", id)
	case <-time.After(20 * time.Millisecond):
	}
	execution.BindID("session-1")
	select {
	case id := <-result:
		if id != "session-1" {
			t.Fatalf("session identity = %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("session identity did not unblock after bind")
	}
}
