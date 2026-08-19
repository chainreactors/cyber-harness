package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	node "github.com/chainreactors/aiscan/pkg/node"
	"github.com/chainreactors/aiscan/pkg/runner"
	_ "github.com/chainreactors/aiscan/tools"
	_ "github.com/chainreactors/aiscan/tools/arsenal"
	_ "github.com/chainreactors/aiscan/tools/gogo"
	_ "github.com/chainreactors/aiscan/tools/ioa"
	_ "github.com/chainreactors/aiscan/tools/neutron"
	_ "github.com/chainreactors/aiscan/tools/proton"
	_ "github.com/chainreactors/aiscan/tools/proxy"
	_ "github.com/chainreactors/aiscan/tools/search"
	_ "github.com/chainreactors/aiscan/tools/spray"
	_ "github.com/chainreactors/aiscan/tools/zombie"
)

type options struct {
	server     string
	token      string
	id         string
	websocket  string
	configFile string
	jsonFrames bool
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "runner:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stderr io.Writer) error {
	options, err := parseOptions(args, stderr)
	if err != nil {
		return err
	}
	option := new(cfg.Option)
	option.ConfigFile = options.configFile
	if _, err := runner.ResolveRuntimeConfig(option); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := telemetry.GlobalLogger(telemetry.LogConfig{
		Debug: option.Debug, Quiet: option.Quiet, Output: stderr, Color: !option.NoColor,
	})
	application, err := newApplication(ctx, option, logger)
	if err != nil {
		return err
	}
	defer application.Close()
	if err := application.WaitEngines(ctx); err != nil {
		return err
	}
	logger.Infof("runner tools ready: %s", strings.Join(application.Commands.Names(), ", "))
	return node.RunToolNode(ctx, node.ToolNodeConfig{
		ServerURL:  options.server,
		WSPath:     options.websocket,
		ID:         options.id,
		Token:      options.token,
		Registry:   application.Commands,
		Events:     application.EventBus,
		Progress:   application.Progress,
		Logger:     logger,
		Version:    cfg.Version,
		JSONFrames: options.jsonFrames,
		FileAudit:  application.FileAudit,
		ExtraNamespaces: []func(*aop.NamespaceMux) error{
			application.RegisterTrafficNamespace,
		},
	})
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	var result options
	flags := flag.NewFlagSet("runner", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&result.server, "server", "", "AOP server URL")
	flags.StringVar(&result.token, "token", "", "server access token")
	flags.StringVar(&result.id, "id", "", "stable runner ID (defaults to hostname)")
	flags.StringVar(&result.websocket, "ws-path", node.DefaultWSPath, "AOP WebSocket path")
	flags.StringVar(&result.configFile, "config", "", "path to aiscan.yaml")
	flags.BoolVar(&result.jsonFrames, "json", false, "use ProtoJSON WebSocket frames")
	if err := flags.Parse(args); err != nil {
		return result, err
	}
	if strings.TrimSpace(result.server) == "" {
		return result, fmt.Errorf("--server is required")
	}
	return result, nil
}

func newApplication(ctx context.Context, option *cfg.Option, logger telemetry.Logger) (*runner.App, error) {
	config := runner.AppConfig(option, runner.RuntimeFeatures{ToolsEnabled: true}, logger)
	config.Tools.RunnerMode = true
	return runner.NewApp(ctx, config)
}
