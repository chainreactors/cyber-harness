package runner

import (
	"context"
	"fmt"
	"io"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/tui"
	rlterm "github.com/chainreactors/tui/readline/terminal"
	"github.com/chainreactors/utils/pty"
)

const MainREPLName = "main-repl"

func (rt *AgentRuntime) startMainREPL() error {
	if rt == nil || rt.app == nil {
		return fmt.Errorf("main repl requires an agent runtime")
	}
	if rt.ptyManager == nil {
		return fmt.Errorf("pty manager unavailable")
	}
	sess, err := rt.OpenSession(rt.ctx, SessionOptions{ID: MainREPLName})
	if err != nil {
		return err
	}
	option := rt.option
	if option == nil {
		option = &cfg.Option{}
	}
	control := rlterm.NewControl(true, 80, 24)
	info, err := rt.ptyManager.CreateInteractiveFuncWithOptions(rt.ctx, MainREPLName, "aiscan repl", pty.InteractiveOptions{
		Timeout:   0,
		StripANSI: false,
		Resize:    control.SetSize,
	}, func(replCtx context.Context, input io.Reader, output io.Writer) error {
		for {
			err := tui.RunRemoteAgentConsoleWithControl(replCtx, option, rt.consoleAppInfoForSession(sess), sess.state.agent, input, output, control, rt.Subscribe)
			if replCtx.Err() != nil {
				return replCtx.Err()
			}
			if err != nil || rt.replMode != REPLPersistent {
				return err
			}
		}
	})
	if err != nil {
		return err
	}
	rt.ptyManager.SetKind(info.ID, "repl")
	return nil
}

// newPTYRouter returns a connection-scoped router over the Runtime-owned PTY
// manager. Closing the router only detaches its monitors; Runtime.Close owns
// session shutdown.
func (rt *AgentRuntime) newPTYRouter() (*pty.Router, error) {
	if rt == nil || rt.ptyManager == nil || rt.ptyManager.Manager == nil {
		return nil, fmt.Errorf("pty manager unavailable")
	}
	openers := pty.DefaultOpeners(rt.ptyManager.Manager, pty.DefaultSessionTimeout, pty.DefaultEnv())
	return pty.NewRouter(rt.ptyManager.Manager, pty.WithOpeners(openers)), nil
}
