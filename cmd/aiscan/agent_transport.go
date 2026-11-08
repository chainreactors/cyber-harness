package main

import (
	"context"
	"fmt"
	"io"

	"github.com/chainreactors/cyber/core/telemetry"
	taskcli "github.com/chainreactors/cyber/pkg/cli/task"
	cfg "github.com/chainreactors/cyber/pkg/config"
	node "github.com/chainreactors/cyber/pkg/node"
	"github.com/chainreactors/cyber/pkg/profile"
)

// runAgentTransport selects exactly one Agent transport. Session, provider and
// PTY state stay inside the single Manager created by that transport.
func runAgentTransport(ctx context.Context, newProfile func(profile.Request) (profile.Profile, error), option *cfg.Option, logger telemetry.Logger, input io.Reader, output io.Writer, setInterrupt func(func() bool), outputTask *taskcli.Output) error {
	selected, err := cfg.ResolveAgentTransport(option)
	if err != nil {
		return err
	}
	switch selected {
	case cfg.AgentTransportWeb:
		if option.OutputFormat != "text" || option.OutputFile != "" {
			return fmt.Errorf("node transport uses the server event stream; -o/-f require a local task")
		}
		return node.RunWebSocketWithCapabilities(ctx, newProfile, option, logger, "scan")
	case cfg.AgentTransportStdio:
		if option.OutputFormat != "text" && option.OutputFile == "" {
			return fmt.Errorf("stdio transport reserves stdout for protocol messages; use -f for formatted events")
		}
		return runStdio(ctx, newProfile, option, logger, input, output, outputTask)
	default:
		return runAgentMode(ctx, newProfile, option, logger, setInterrupt, outputTask)
	}
}
