package console

import (
	"context"
	"fmt"
	"io"

	agentsession "github.com/chainreactors/cyber/agent/session"
	tmuxpkg "github.com/chainreactors/cyber/agent/tmux"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/pkg/commands"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	rlterm "github.com/chainreactors/tui/readline/terminal"
	"github.com/chainreactors/utils/pty"
)

const MainREPLName = "main-repl"

// REPL owns the console task's cancellation and completion. Its Runtime and
// Bash manager are profile-owned; neither is closed when the console detaches.
type REPL struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func StartPersistent(rt *agentsession.Runtime, option *cfg.Option, bindings *consoleapi.Bindings) (*REPL, error) {
	if rt == nil || rt.App() == nil {
		return nil, fmt.Errorf("main repl requires a runtime")
	}
	manager := bashManager(rt.App().Bash)
	if manager == nil {
		return nil, fmt.Errorf("pty manager unavailable")
	}
	ctx, cancel := context.WithCancel(rt.Context())
	r := &REPL{cancel: cancel, done: make(chan struct{})}
	session, err := rt.OpenSession(ctx, agentsession.SessionOptions{ID: MainREPLName})
	if err != nil {
		cancel()
		return nil, err
	}
	if option == nil {
		option = &cfg.Option{}
	}
	control := rlterm.NewControl(true, 80, 24)
	info, err := manager.CreateInteractiveFuncWithOptions(ctx, MainREPLName, "cyber repl", pty.InteractiveOptions{
		Timeout: 0, StripANSI: false, Resize: control.SetSize,
	}, func(replCtx context.Context, input io.Reader, output io.Writer) error {
		defer close(r.done)
		defer func() { _ = rt.CloseSession(context.Background(), MainREPLName, agentsession.SessionCloseCompleted) }()
		for {
			err := runRemoteConsole(replCtx, rt, session, option, input, output, control, bindings)
			if replCtx.Err() != nil {
				return replCtx.Err()
			}
			if err != nil {
				return err
			}
		}
	})
	if err != nil {
		cancel()
		_ = rt.CloseSession(context.Background(), MainREPLName, agentsession.SessionCloseError)
		return nil, err
	}
	manager.SetKind(info.ID, "repl")
	return r, nil
}

func (r *REPL) Close() {
	if r == nil {
		return
	}
	r.cancel()
	<-r.done
}

func bashManager(bash *commands.BashTool) *tmuxpkg.Manager {
	if bash == nil {
		return nil
	}
	return bash.Manager()
}
