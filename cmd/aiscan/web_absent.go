//go:build !full

package main

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/core/telemetry"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func serveWeb(context.Context, *cfg.Option, *cfg.Option, webCommand, telemetry.Logger) error {
	return fmt.Errorf("web server not available (requires full build)")
}
