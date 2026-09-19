//go:build !full

package main

import (
	"context"
	"fmt"

	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
)

func serveWeb(context.Context, *cfg.Option, *cfg.Option, webCommand, telemetry.Logger) error {
	return fmt.Errorf("web server not available (requires full build)")
}
