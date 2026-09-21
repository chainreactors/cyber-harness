package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/chainreactors/cyber/core/egress"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	toolnode "github.com/chainreactors/cyber/pkg/node/tool"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"

	"github.com/chainreactors/cyber/core/hooks"
)

func newRegistry(workDir string) (coretool.Executor, *extension.Set) {
	registry := coretool.NewToolRegistry()
	set, err := extension.New(
		extension.Provided[*hooks.Registry](hooks.New()),
		extension.Provided[egress.Endpoint](egress.Disabled()),
		coretool.NewCommandRegistry(), registry,
		terminalext.New(terminalext.Config{Directory: workDir, Timeout: 300}),
	)
	if err != nil {
		panic(err)
	}
	if err := set.Load(context.Background()); err != nil {
		_ = set.Close(context.Background())
		panic(err)
	}
	return registry, set
}

func main() {
	if code, handled := terminaltool.RunShellCommandProxy(); handled {
		os.Exit(code)
	}
	var (
		serverURL string
		token     string
		nodeID    string
		wsPath    string
	)
	flag.StringVar(&serverURL, "server", "", "AOP hub URL, e.g. http://host:8080")
	flag.StringVar(&token, "token", "", "hub access token")
	flag.StringVar(&nodeID, "id", "", "stable node ID (default: hostname)")
	flag.StringVar(&wsPath, "ws-path", toolnode.DefaultWSPath, "AOP WebSocket path")
	flag.Parse()
	if serverURL == "" {
		fmt.Fprintln(os.Stderr, "usage: rmcp --server <url> [--token <token>] [--id <node-id>]")
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	logger := telemetry.GlobalLogger(telemetry.LogConfig{Output: os.Stderr})

	workDir, _ := os.Getwd()
	tools, set := newRegistry(workDir)
	defer set.Close(context.Background())

	logger.Infof("rmcp tools ready: bash (workdir %s)", workDir)
	if err := toolnode.Run(ctx, toolnode.Config{
		ServerURL: serverURL,
		WSPath:    wsPath,
		ID:        nodeID,
		Token:     token,
		Executor:  tools,
		Logger:    logger,
		Version:   "rmcp-example",
	}); err != nil {
		logger.Errorf("rmcp: %v", err)
		os.Exit(1)
	}
}
