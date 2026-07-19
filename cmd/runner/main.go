package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/node"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
)

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "__tool" {
		os.Exit(runToolMode(os.Args[2:]))
	}

	var (
		serverURL  string
		token      string
		name       string
		wsPath     string
		configFile string
	)
	flag.StringVar(&serverURL, "server", "", "server url, e.g. http://host:8080")
	flag.StringVar(&token, "token", "", "enrollment token (passed as URL userinfo)")
	flag.StringVar(&name, "name", "", "runner display name (default: hostname)")
	flag.StringVar(&wsPath, "ws-path", "/ws/runner", "WebSocket endpoint path")
	flag.StringVar(&configFile, "config", "", "path to aiscan.yaml (for cyberhub settings)")
	flag.Parse()
	if serverURL == "" {
		fmt.Fprintln(os.Stderr, "usage: aiscan-runner --server <url> [--token <token>]")
		os.Exit(2)
	}

	if name == "" {
		name, _ = os.Hostname()
	}

	// Embed token into URL userinfo so webagent extracts it as Bearer token.
	if token != "" && !strings.Contains(serverURL, "@") {
		serverURL = embedToken(serverURL, token)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; cancel() }()

	logger := telemetry.GlobalLogger(telemetry.LogConfig{Output: os.Stderr})

	var option cfg.Option
	if configFile != "" {
		option.ConfigFile = configFile
		_ = os.Setenv("AISCAN_RUNNER_CONFIG", configFile)
	}
	if _, err := cfg.ResolveRuntimeConfig(&option); err != nil {
		logger.Errorf("load config: %v", err)
		os.Exit(1)
	}

	dataBus := eventbus.New[output.ToolDataEvent]()
	registry, err := initTools(ctx, &option, logger, dataBus)
	if err != nil {
		logger.Errorf("init tools: %v", err)
		os.Exit(1)
	}
	logger.Infof("tools ready: %s", strings.Join(registry.Names(), ", "))
	cleanupWrappers, err := installToolWrappers(registry)
	if err != nil {
		logger.Warnf("install tool wrappers: %v", err)
	} else {
		defer cleanupWrappers()
	}

	scoSidecar := output.NewSCOSidecar(dataBus, output.CSTXTransform)
	defer scoSidecar.Close()

	bus := eventbus.New[agent.Event]()
	if err := node.Connect(ctx, node.ConnectConfig{
		ServerURL: serverURL,
		WSPath:    wsPath,
		Name:      name,
		Registry:  registry,
		AgentBus:  bus,
		DataBus:   dataBus,
		SCO:       scoSidecar,
		Logger:    logger,
	}); err != nil {
		logger.Errorf("runner: %v", err)
		os.Exit(1)
	}
}

func runToolMode(args []string) int {
	ctx := context.Background()
	logger := telemetry.GlobalLogger(telemetry.LogConfig{Output: os.Stderr})
	var option cfg.Option
	if configFile := strings.TrimSpace(os.Getenv("AISCAN_RUNNER_CONFIG")); configFile != "" {
		option.ConfigFile = configFile
	}
	if _, err := cfg.ResolveRuntimeConfig(&option); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	dataBus := eventbus.New[output.ToolDataEvent]()
	registry, err := initTools(ctx, &option, logger, dataBus)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	out, err := registry.ExecuteArgsStreaming(ctx, args, os.Stdout)
	if out != "" {
		fmt.Fprint(os.Stdout, out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Fprintln(os.Stdout)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func installToolWrappers(registry *commands.CommandRegistry) (func(), error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "aiscan-runner-tools-")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	names := append([]string(nil), registry.Names()...)
	sort.Strings(names)
	for _, name := range names {
		if !validToolName(name) {
			continue
		}
		if err := writeToolWrapper(dir, executable, name); err != nil {
			cleanup()
			return nil, err
		}
	}
	pathValue := dir
	if current := os.Getenv("PATH"); current != "" {
		pathValue += string(os.PathListSeparator) + current
	}
	if err := os.Setenv("PATH", pathValue); err != nil {
		cleanup()
		return nil, err
	}
	return cleanup, nil
}

func writeToolWrapper(dir, executable, name string) error {
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, name+".cmd")
		body := fmt.Sprintf("@\"%s\" __tool %s %%*\r\n", strings.ReplaceAll(executable, "\"", "\"\""), name)
		return os.WriteFile(path, []byte(body), 0o755)
	}
	path := filepath.Join(dir, name)
	body := fmt.Sprintf("#!/bin/sh\nexec %s __tool %s \"$@\"\n", shellSingleQuote(executable), shellSingleQuote(name))
	return os.WriteFile(path, []byte(body), 0o755)
}

func validToolName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
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

	reg := commands.NewRegistry()
	deps := &commands.Deps{
		WorkDir:   wd(),
		EngineSet: engineSet,
		Logger:    logger,
		DataBus:   dataBus,
	}
	if engineSet != nil {
		deps.Resources = engineSet.Resources
		deps.ScannerProxy = option.Proxy
	}
	for _, group := range []string{"core", "scanner", "arsenal"} {
		commands.BuildGroup(group, deps, reg)
	}
	reg.SetLogger(logger)
	return reg, nil
}

func embedToken(serverURL, token string) string {
	// http://host:8080 → http://TOKEN@host:8080
	for _, scheme := range []string{"https://", "http://", "wss://", "ws://"} {
		if strings.HasPrefix(serverURL, scheme) {
			return scheme + token + "@" + serverURL[len(scheme):]
		}
	}
	return token + "@" + serverURL
}

func wd() string {
	d, _ := os.Getwd()
	return d
}
