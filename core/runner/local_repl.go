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
	if rt == nil || rt.App == nil {
		return fmt.Errorf("local repl requires an agent runtime")
	}
	sess, err := rt.session(MainREPLName)
	if err != nil {
		return err
	}
	option := rt.Option
	if option == nil {
		option = &cfg.Option{}
	}
	return tui.RunAgentConsoleWithTerminal(
		ctx,
		option,
		rt.consoleAppInfo(),
		sess.agent,
		rlterm.Local(),
		rt.Bus.Subscribe,
	)
}
