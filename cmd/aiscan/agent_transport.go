package main

import (
	"context"
	"io"

	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
	node "github.com/chainreactors/cyber/pkg/node"
	"github.com/chainreactors/cyber/pkg/profile"
	"github.com/chainreactors/cyber/pkg/runner"
)

// runAgentTransport selects exactly one Agent transport. Session, provider and
// PTY state stay inside the single Manager created by that transport.
func runAgentTransport(ctx context.Context, newProfile func(profile.Request) (profile.Profile, error), option *cfg.Option, logger telemetry.Logger, input io.Reader, output io.Writer, setInterrupt func(func() bool)) error {
	selected, err := cfg.ResolveAgentTransport(option)
	if err != nil {
		return err
	}
	switch selected {
	case cfg.AgentTransportWeb:
		return node.RunWebSocket(ctx, newProfile, option, logger)
	case cfg.AgentTransportStdio:
		return runner.RunStdio(ctx, newProfile, option, logger, input, output)
	default:
		return runner.RunAgentMode(ctx, newProfile, option, logger, setInterrupt)
	}
}
