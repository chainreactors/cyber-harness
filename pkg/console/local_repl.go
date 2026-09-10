package console

import (
	"context"
	"fmt"

	cfg "github.com/chainreactors/aiscan/core/config"
	runtimepkg "github.com/chainreactors/aiscan/pkg/runtime"
	rlterm "github.com/chainreactors/tui/readline/terminal"
)

// AttachLocalREPL runs the ephemeral console directly on the process terminal.
//
// Readline control sequences cannot pass through the runtime PTY output buffer:
// attach replays buffered bytes, which can re-execute stale cursor state and
// corrupt native scrollback. Persistent remote REPLs continue to use the PTY;
// the ephemeral local console binds directly to the process terminal.
func AttachLocalREPL(ctx context.Context, rt *runtimepkg.AgentRuntime, option *cfg.Option) error {
	if rt == nil || rt.App() == nil {
		return fmt.Errorf("local repl requires an agent runtime")
	}
	if ctx == nil {
		ctx = rt.Context()
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(rt.Context(), cancel)
	defer stop()
	defer cancel()
	sess, err := rt.OpenSession(ctx, runtimepkg.SessionOptions{ID: MainREPLName})
	if err != nil {
		return err
	}
	defer rt.CloseSession(context.Background(), MainREPLName, runtimepkg.SessionCloseCompleted)
	if option == nil {
		option = &cfg.Option{}
	}
	return newAgentConsole(ctx, rt, sess, option, rlterm.Local()).Start()
}
