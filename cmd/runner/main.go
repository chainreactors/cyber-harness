package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/chainreactors/aiscan/pkg/webagent"
)

func main() {
	var (
		serverURL  string
		token      string
		runnerID   string
		wsPath     string
		configFile string
	)
	flag.StringVar(&serverURL, "server", "", "Cairn server URL, e.g. http://host:8080")
	flag.StringVar(&token, "token", "", "runner token")
	flag.StringVar(&runnerID, "id", "", "stable runner ID (default: hostname)")
	flag.StringVar(&wsPath, "ws-path", "/ws/runner", "runner WebSocket path")
	flag.StringVar(&configFile, "config", "", "path to aiscan.yaml")
	flag.Parse()
	if serverURL == "" || token == "" {
		fmt.Fprintln(os.Stderr, "usage: aiscan-runner --server <url> --token <token> [--id <runner-id>]")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	logger := telemetry.GlobalLogger(telemetry.LogConfig{Output: os.Stderr})

	option := &cfg.Option{}
	option.ConfigFile = configFile
	if _, err := cfg.ResolveRuntimeConfig(option); err != nil {
		logger.Errorf("load config: %v", err)
		os.Exit(1)
	}
	dataBus := eventbus.New[output.ToolDataEvent]()
	registry, err := initTools(ctx, option, logger, dataBus)
	if err != nil {
		logger.Errorf("initialize tools: %v", err)
		os.Exit(1)
	}
	defer closeTools(registry)

	sco := output.NewSCOSidecar(dataBus, output.CSTXTransform)
	defer sco.Close()
	logger.Infof("tools ready: %s", strings.Join(registry.Names(), ", "))
	if err := webagent.RunToolNode(ctx, webagent.ToolNodeConfig{
		ServerURL: serverURL,
		WSPath:    wsPath,
		ID:        runnerID,
		Token:     token,
		Registry:  registry,
		DataBus:   dataBus,
		SCO:       sco,
		Logger:    logger,
		Version:   cfg.Version,
	}); err != nil {
		logger.Errorf("runner: %v", err)
		os.Exit(1)
	}
}

func initTools(ctx context.Context, option *cfg.Option, logger telemetry.Logger, dataBus *eventbus.Bus[output.ToolDataEvent]) (*commands.CommandRegistry, error) {
	engineSet, err := engine.InitWithOptions(ctx, resources.Options{
		CyberhubURL: option.CyberhubURL,
		APIKey:      option.CyberhubKey,
		Mode:        option.CyberhubMode,
		Proxy:       option.Proxy,
	}, logger)
	if err != nil {
		logger.Warnf("engine init: %v (continuing with available engines)", err)
	}

	workDir, _ := os.Getwd()
	registry := commands.NewRegistry()
	deps := &commands.Deps{
		WorkDir:      workDir,
		RunnerMode:   true,
		EngineSet:    engineSet,
		Logger:       logger,
		DataBus:      dataBus,
		ScannerProxy: option.Proxy,
	}
	if engineSet != nil {
		deps.Resources = engineSet.Resources
	}
	for _, group := range []string{"core", "scanner", "arsenal"} {
		commands.BuildGroup(group, deps, registry)
	}
	registry.SetLogger(logger)
	return registry, nil
}

func closeTools(registry *commands.CommandRegistry) {
	if registry == nil {
		return
	}
	for _, tool := range registry.Tools() {
		if closer, ok := tool.(interface{ Close() }); ok {
			closer.Close()
		}
	}
	for _, command := range registry.All() {
		if command.Close != nil {
			command.Close()
		}
	}
}
