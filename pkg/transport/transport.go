package transport

import (
	"context"
	"io"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/runner"
	node "github.com/chainreactors/aiscan/pkg/node"
)

// Run selects exactly one Agent transport. Session, provider and PTY state stay
// inside the single AgentRuntime created by that transport.
func Run(ctx context.Context, option *cfg.Option, logger telemetry.Logger, input io.Reader, output io.Writer, setInterrupt func(func() bool)) error {
	selected, err := cfg.ResolveAgentTransport(option)
	if err != nil {
		return err
	}
	switch selected {
	case cfg.AgentTransportWeb:
		return node.RunWebSocket(ctx, option, logger)
	case cfg.AgentTransportStdio:
		return runner.RunStdio(ctx, option, logger, input, output)
	default:
		return runner.RunAgentMode(ctx, option, logger, setInterrupt)
	}
}
