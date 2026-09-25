package proxy

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/tools/terminal"
	"github.com/chainreactors/utils/proc"
)

func TestProxyDetachedTmuxRetainsRoute(t *testing.T) {
	state := NewState("")
	resource := NewProxyHub(state, nil, t.TempDir(), false, nil)
	if err := resource.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer resource.Close(context.Background())
	bash := terminal.NewBashTool(t.TempDir(), 30, nil).WithEnvironment(map[string]string{"PROXY_SESSION_CHILD": "1"})
	bash.SetEgressResolver(resource.ProxyHub.Egress)
	defer bash.Close()
	tmux := terminal.NewTmuxCommand(bash)
	command := New(state)
	command.SetHub(resource.ProxyHub)
	command.SetCommandExecutor(func(ctx context.Context, argv []string, execution *coretool.Execution) (any, error) {
		if argv[0] != "tmux" {
			return nil, fmt.Errorf("unexpected command %q", argv[0])
		}
		return tmux.Run(ctx, &coretool.Execution{
			Args: argv[1:], Env: execution.Env, Route: execution.Route,
			OnBackground: execution.OnBackground, Stdout: execution.Stdout, Stderr: execution.Stderr,
		})
	})
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf("%q -test.run=^TestProxySessionChild$", program)
	var output bytes.Buffer
	_, err = command.Run(t.Context(), &coretool.Execution{
		Args: []string{"http://127.0.0.1:1", "tmux", "new-session", "-d", line}, Stdout: &output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "[detached]") {
		t.Fatalf("tmux output = %q", output.String())
	}
	sessions := bash.Manager().List()
	if len(sessions) != 1 || sessions[0].Shape != proc.ShapeTTY {
		t.Fatalf("sessions = %+v, want one PTY", sessions)
	}
	outputDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(outputDeadline) {
		if strings.Contains(bash.Manager().PeekOrEmpty(sessions[0].ID, 10), "@127.0.0.1:") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if output := bash.Manager().PeekOrEmpty(sessions[0].ID, 10); !strings.Contains(output, "@127.0.0.1:") {
		t.Fatalf("detached child did not inherit route: %q", output)
	}
	resource.ProxyHub.correlationMu.RLock()
	active := len(resource.ProxyHub.correlations)
	resource.ProxyHub.correlationMu.RUnlock()
	if active != 1 {
		t.Fatalf("live route leases = %d, want 1", active)
	}
	if err := bash.Manager().Kill(sessions[0].ID); err != nil {
		t.Fatal(err)
	}
	<-bash.Manager().Done(sessions[0].ID)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resource.ProxyHub.correlationMu.RLock()
		active = len(resource.ProxyHub.correlations)
		resource.ProxyHub.correlationMu.RUnlock()
		if active == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("route lease remains after session exit: %d", active)
}

func TestProxySessionChild(t *testing.T) {
	if os.Getenv("PROXY_SESSION_CHILD") != "1" {
		return
	}
	fmt.Println("route:", os.Getenv("HTTP_PROXY"))
	time.Sleep(15 * time.Second)
}
