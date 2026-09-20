package console

import (
	"context"
	"fmt"

	agentsession "github.com/chainreactors/cyber/agent/session"
	cfg "github.com/chainreactors/cyber/pkg/config"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	rlterm "github.com/chainreactors/tui/readline/terminal"
)

// AttachLocalREPL runs the ephemeral console directly on the process terminal.
//
// Readline control sequences cannot pass through the runtime PTY output buffer:
// attach replays buffered bytes, which can re-execute stale cursor state and
// corrupt native scrollback. Persistent remote REPLs continue to use the PTY;
// the ephemeral local console binds directly to the process terminal.
func AttachLocalREPL(ctx context.Context, rt *agentsession.Runtime, option *cfg.Option, bindings *consoleapi.Bindings) error {
	if !rt.Active() {
		return fmt.Errorf("local repl requires an agent runtime")
	}
	if ctx == nil {
		ctx = rt.Context()
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(rt.Context(), cancel)
	defer stop()
	defer cancel()
	sess, err := rt.OpenSession(ctx, agentsession.SessionOptions{ID: MainREPLName})
	if err != nil {
		return err
	}
	defer func() { _ = rt.CloseSession(context.Background(), MainREPLName, agentsession.SessionCloseCompleted) }()
	if option == nil {
		option = &cfg.Option{}
	}
	return newAgentConsole(ctx, rt, sess, option, rlterm.Local(), bindings).Start()
}
