package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
	toolsext "github.com/chainreactors/aiscan/pkg/exts/tools"
	"github.com/chainreactors/aiscan/pkg/toolnode"
	"github.com/chainreactors/aiscan/pkg/toolset"
)

func newRegistry(workDir string) (tool.Executor, *commands.BashTool, *extension.Set) {
	bash := commands.NewBashTool(workDir, 300, nil)
	registry := toolset.NewRegistry(nil)
	ext, err := toolsext.New(registry, bash)
	if err != nil {
		panic(err)
	}
	set, err := extension.New(
		extension.Entry{ID: "tools", Extension: ext},
		extension.Entry{ID: "tool-registry", DependsOn: []string{"tools"}, Extension: registry},
	)
	if err != nil {
		panic(err)
	}
	if err := set.Load(context.Background()); err != nil {
		_ = set.Close(context.Background())
		panic(err)
	}
	return registry, bash, set
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
	tools, bash, set := newRegistry(workDir)
	defer bash.Close()
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
