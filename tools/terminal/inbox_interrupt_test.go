package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent/inbox"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/utils/proc"
)

func TestInboxInterruptDetachesTmuxExecution(t *testing.T) {
	for _, command := range []string{"hold", "tmux new-session hold", "tmux new-session -d hold"} {
		t.Run(command, func(t *testing.T) {
			bash := NewBashTool(t.TempDir(), 5, nil)
			defer bash.Close()
			started, release, stopped := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			registry, _ := loadTestRegistry(t, commandBatch(
				NewTmuxCommand(bash),
				coretool.Command{Name: "hold", Usage: "hold", Run: func(ctx context.Context, execution *coretool.Execution) (any, error) {
					close(started)
					select {
					case <-release:
						fmt.Fprint(execution.Stdout, "finished once")
						stopped <- nil
						return nil, nil
					case <-ctx.Done():
						stopped <- ctx.Err()
						return nil, ctx.Err()
					}
				}},
			))
			bash.SetCommandRegistry(registry)
			ib := inbox.NewBuffered(16)
			defer ib.Close()
			ctx, cancel := context.WithCancel(inbox.ContextWithInbox(t.Context(), ib))
			defer cancel()
			args, _ := json.Marshal(BashArgs{Command: command})
			done := make(chan string, 1)
			go func() {
				result, err := bash.Execute(ctx, string(args))
				if err != nil {
					done <- err.Error()
				} else {
					done <- coretool.ResultText(result)
				}
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("command did not start")
			}
			m := inbox.NewUserMessage("urgent input")
			m.Interrupt = true
			if err := ib.Push(m); err != nil {
				t.Fatal(err)
			}
			var result string
			select {
			case result = <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("foreground wait was not interrupted")
			}
			if !strings.Contains(result, "background") && !strings.Contains(result, "detached") {
				t.Fatalf("result=%q", result)
			}
			if ib.ActiveProducers() != 1 {
				t.Fatalf("monitors=%d; result=%q", ib.ActiveProducers(), result)
			}
			cancel() // Returning/canceling the former foreground call must not kill the command.
			select {
			case err := <-stopped:
				t.Fatalf("background command stopped: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			close(release)
			select {
			case err := <-stopped:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("command did not finish")
			}
			wait, stop := context.WithTimeout(t.Context(), 3*time.Second)
			defer stop()
			completions := 0
			for ib.WaitWhileActive(wait) {
				for _, msg := range ib.Drain() {
					if msg.Meta["type"] == "completion" {
						completions++
						id, _ := msg.Meta["session_id"].(string)
						if !strings.Contains(result, id) {
							t.Fatalf("foreground/background identity changed: %q -> %q", result, id)
						}
					}
				}
			}
			if completions != 1 || ib.ActiveProducers() != 0 {
				t.Fatalf("completions=%d producers=%d", completions, ib.ActiveProducers())
			}
		})
	}
}

func TestInboxInterruptRealShellKeepsTmuxSession(t *testing.T) {
	command := "sleep 2"
	if runtime.GOOS == "windows" {
		command = "ping -n 3 127.0.0.1"
	}
	bash := NewBashTool(t.TempDir(), 5, nil)
	defer bash.Close()
	ib := inbox.NewBuffered(16)
	defer ib.Close()
	ctx, cancel := context.WithCancel(inbox.ContextWithInbox(t.Context(), ib))
	defer cancel()
	execution, err := bash.Start(ctx, command, BashExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	id := execution.ID
	m := inbox.NewUserMessage("handle this now")
	m.Interrupt = true
	if err = ib.Push(m); err != nil {
		t.Fatal(err)
	}
	result := bash.waitOrBackground(execution, ctx, ib, 0)
	if !strings.Contains(coretool.ResultText(result), "background") || !strings.Contains(coretool.ResultText(result), id) {
		t.Fatalf("foreground result = %s", coretool.ResultText(result))
	}
	cancel()
	wait, stop := context.WithTimeout(t.Context(), 6*time.Second)
	defer stop()
	if err = execution.WaitProcessCompletion(wait); err != nil {
		t.Fatal(err)
	}
	info, ok := bash.Manager().Get(id)
	if !ok || info.State != proc.StateCompleted || info.ExitStatus() != 0 {
		t.Fatalf("shell was restarted or canceled: %+v", info)
	}
}
