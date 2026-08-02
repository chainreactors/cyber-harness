package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	node "github.com/chainreactors/aiscan/pkg/node"
)

func newRegistry(workDir string) *commands.CommandRegistry {
	registry := commands.NewRegistry()
	bash := commands.NewBashTool(workDir, 300)
	bash.SetCommandNames(registry.Names)
	bash.SetCommandResolver(registry.Get)
	registry.RegisterTool(bash)
	return registry
}

func main() {
	var (
		serverURL string
		token     string
		nodeID    string
		wsPath    string
	)
	flag.StringVar(&serverURL, "server", "", "AOP hub URL, e.g. http://host:8080")
	flag.StringVar(&token, "token", "", "hub access token")
	flag.StringVar(&nodeID, "id", "", "stable node ID (default: hostname)")
	flag.StringVar(&wsPath, "ws-path", node.DefaultWSPath, "AOP WebSocket path")
	flag.Parse()
	if serverURL == "" {
		fmt.Fprintln(os.Stderr, "usage: rmcp --server <url> [--token <token>] [--id <node-id>]")
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	logger := telemetry.GlobalLogger(telemetry.LogConfig{Output: os.Stderr})

	workDir, _ := os.Getwd()
	registry := newRegistry(workDir)
	registry.SetLogger(logger)

	logger.Infof("rmcp tools ready: bash (workdir %s)", workDir)
	if err := node.RunToolNode(ctx, node.ToolNodeConfig{
		ServerURL: serverURL,
		WSPath:    wsPath,
		ID:        nodeID,
		Token:     token,
		Registry:  registry,
		Logger:    logger,
		Version:   "rmcp-example",
	}); err != nil {
		logger.Errorf("rmcp: %v", err)
		os.Exit(1)
	}
}
