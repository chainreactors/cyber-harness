package runner

import (
	"context"
	"fmt"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/tui"
	rlterm "github.com/chainreactors/tui/readline/terminal"
)

// AttachLocalREPL runs the ephemeral console directly on the process terminal.
//
// Readline control sequences cannot pass through the runtime PTY output buffer:
// attach replays buffered bytes, which can re-execute stale cursor state and
// corrupt native scrollback. Persistent remote REPLs continue to use the PTY;
// the ephemeral local console binds directly to the process terminal.
func (rt *AgentRuntime) AttachLocalREPL(ctx context.Context) error {
	if rt == nil || rt.app == nil {
		return fmt.Errorf("local repl requires an agent runtime")
	}
	sess, err := rt.OpenSession(ctx, SessionOptions{ID: MainREPLName})
	if err != nil {
		return err
	}
	option := rt.option
	if option == nil {
		option = &cfg.Option{}
	}
	return tui.RunAgentConsoleWithTerminal(
		ctx,
		option,
		rt.consoleAppInfoForSession(sess),
		sess.state.agent,
		rlterm.Local(),
		rt.Subscribe,
	)
}
