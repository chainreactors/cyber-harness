package console

import (
	"context"
	"fmt"
	agentsession "github.com/chainreactors/cyber/agent/session"
	procbus "github.com/chainreactors/cyber/core/proc"
	cfg "github.com/chainreactors/cyber/pkg/config"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
	rlterm "github.com/chainreactors/tui/readline/terminal"
	"github.com/chainreactors/utils/proc"
	"io"
)

const MainREPLName = "main-repl"

// REPL owns the console task's cancellation and completion. Its Runtime and
// Bash manager are profile-owned; neither is closed when the console detaches.
type REPL struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func StartPersistent(rt *agentsession.Runtime, option *cfg.Option, bindings *consoleapi.Bindings) (*REPL, error) {
	if !rt.Configured() {
		return nil, fmt.Errorf("main repl requires a runtime")
	}
	manager := bashManager(rt.Bash())
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
	// No timeout: the REPL lives as long as its context does.
	_, err = manager.Start(ctx, proc.Spec{Name: MainREPLName, Kind: "repl", Command: "cyber repl"},
		proc.IOFunc(func(replCtx context.Context, input io.Reader, output io.Writer) error {
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
		}, func(cols, rows int) error { control.SetSize(cols, rows); return nil }))
	if err != nil {
		cancel()
		_ = rt.CloseSession(context.Background(), MainREPLName, agentsession.SessionCloseError)
		return nil, err
	}
	return r, nil
}

func (r *REPL) Close() {
	if r == nil {
		return
	}
	r.cancel()
	<-r.done
}

func bashManager(bash *terminaltool.BashTool) *procbus.Manager {
	if bash == nil {
		return nil
	}
	return bash.Manager()
}
