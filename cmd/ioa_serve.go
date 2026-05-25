package cmd

import (
	"context"

	"github.com/chainreactors/aiscan/cmd/ioaserve"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

func runIOAServe(ctx context.Context, option *Option, logger telemetry.Logger) error {
	return ioaserve.RunServe(ctx, ioaserve.Config{
		URL: option.IOAURL,
		DB:  "",
	}, logger)
}
