package console

import (
	"context"
	"fmt"
	"os"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	ioaclient "github.com/chainreactors/ioa/client"
)

func RunIOAClientCommand(ctx context.Context, mode cfg.RunMode, option *cfg.Option, args cfg.IOAClientArgs, logger telemetry.Logger) error {
	ioaURL := option.IOAURL
	if ioaURL == "" {
		ioaURL = "http://127.0.0.1:8765"
	}
	client, err := ioaclient.NewClient(ioaURL, "")
	if err != nil {
		return fmt.Errorf("connect to server: %w", err)
	}
	if client.AccessKey() != "" {
		if err := client.EnsureRegistered(ctx, "aiscan-cli", "", nil); err != nil {
			return fmt.Errorf("server auth register: %w", err)
		}
	}
	switch mode {
	case cfg.RunModeIOASpaces:
		return RunIOASpaces(ctx, client, option, os.Stdout, os.Stderr)
	case cfg.RunModeIOAMessages:
		return RunIOAMessages(ctx, client, option, args, os.Stdout, os.Stderr)
	case cfg.RunModeIOAContext:
		return RunIOAContext(ctx, client, option, args, os.Stdout, os.Stderr)
	case cfg.RunModeIOANodes:
		return RunIOANodes(ctx, client, option, args, os.Stdout, os.Stderr)
	default:
		return fmt.Errorf("unknown server mode: %s", mode)
	}
}
