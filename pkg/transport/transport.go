package transport

import (
	"context"
	"io"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	node "github.com/chainreactors/aiscan/pkg/node"
	"github.com/chainreactors/aiscan/pkg/profile"
	"github.com/chainreactors/aiscan/pkg/runner"
)

// Run selects exactly one Agent transport. Session, provider and PTY state stay
// inside the single Manager created by that transport.
func Run(ctx context.Context, factory profile.Factory, option *cfg.Option, logger telemetry.Logger, input io.Reader, output io.Writer, setInterrupt func(func() bool)) error {
	selected, err := cfg.ResolveAgentTransport(option)
	if err != nil {
		return err
	}
	switch selected {
	case cfg.AgentTransportWeb:
		return node.RunWebSocket(ctx, factory, option, logger)
	case cfg.AgentTransportStdio:
		return runner.RunStdio(ctx, factory, option, logger, input, output)
	default:
		return runner.RunAgentMode(ctx, factory, option, logger, setInterrupt)
	}
}
