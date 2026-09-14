package console

import (
	"context"
	"fmt"
	"io"

	tmuxpkg "github.com/chainreactors/aiscan/agent/tmux"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/commands"
	consoleapi "github.com/chainreactors/aiscan/pkg/console/api"
	agentext "github.com/chainreactors/aiscan/pkg/exts/session"
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

func StartPersistent(rt *agentext.Runtime, option *cfg.Option, bindings *consoleapi.Bindings) (*REPL, error) {
	if rt == nil || rt.App() == nil {
		return nil, fmt.Errorf("main repl requires a runtime")
	}
	manager := bashManager(rt.App().Bash)
	if manager == nil {
		return nil, fmt.Errorf("pty manager unavailable")
	}
	ctx, cancel := context.WithCancel(rt.Context())
	r := &REPL{cancel: cancel, done: make(chan struct{})}
	session, err := rt.OpenSession(ctx, agentext.SessionOptions{ID: MainREPLName})
	if err != nil {
		cancel()
		return nil, err
	}
	if option == nil {
		option = &cfg.Option{}
	}
	control := rlterm.NewControl(true, 80, 24)
	info, err := manager.CreateInteractiveFuncWithOptions(ctx, MainREPLName, "aiscan repl", pty.InteractiveOptions{
		Timeout: 0, StripANSI: false, Resize: control.SetSize,
	}, func(replCtx context.Context, input io.Reader, output io.Writer) error {
		defer close(r.done)
		defer rt.CloseSession(context.Background(), MainREPLName, agentext.SessionCloseCompleted)
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
		_ = rt.CloseSession(context.Background(), MainREPLName, agentext.SessionCloseError)
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
