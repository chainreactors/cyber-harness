package runner

import (
	"context"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
)

// ScannerInitFunc initializes scanner engines and registers scanner commands.
// Set via init() from the package imported by cmd/aiscan.
var ScannerInitFunc func(ctx context.Context, a *App, rc ApplicationConfig, logger telemetry.Logger)

// ScannerWithAgentFunc runs a scanner command with AI agent assistance.
// Set via init() from the package imported by cmd/aiscan.
var ScannerWithAgentFunc func(ctx context.Context, option *cfg.Option, application *App, scannerArgs []string, logger telemetry.Logger) error

// IOAServeFunc starts the IOA HTTP server.
// Set via init() from cmd/aiscan setup.
var IOAServeFunc func(ctx context.Context, option *cfg.Option, logger telemetry.Logger) error

// IOAClientCommandFunc dispatches IOA client CLI commands (spaces, messages, etc.).
// Set via init() from cmd/aiscan setup.
var IOAClientCommandFunc func(ctx context.Context, mode cfg.RunMode, option *cfg.Option, args cfg.IOAClientArgs, logger telemetry.Logger) error
