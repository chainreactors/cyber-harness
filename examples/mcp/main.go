// A no-model MCP-to-CLI host, using the same command registry as AI bash.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	coretool "github.com/chainreactors/cyber/core/tool"
	mcpext "github.com/chainreactors/cyber/exts/mcp"
)

func main() {
	configPath := flag.String("config", "", "MCP JSON config file (mcpServers)")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, *configPath, flag.Args(), os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, configPath string, argv []string, output, diagnostics io.Writer) (err error) {
	if configPath == "" {
		return fmt.Errorf("usage: go run ./examples/mcp -config mcp.json [mcp-ida tools | mcp-ida call <tool> --json '{...}']")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var config mcpext.Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("MCP config must contain one JSON object")
	}
	if config.MCPServers == nil {
		return fmt.Errorf("MCP config requires mcpServers")
	}
	r := coretool.NewCommandRegistry()
	set, err := extension.New(extension.Provided[*hooks.Registry](hooks.New()), r, mcpext.New(config))
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err = errors.Join(err, set.Close(closeCtx))
	}()
	if err := set.Load(ctx); err != nil {
		return err
	}
	if len(argv) == 0 {
		_, err := io.WriteString(output, r.UsageDocs())
		return err
	}
	// Reject unregistered names rather than execute arbitrary PATH programs.
	if !r.Has(argv[0]) {
		return fmt.Errorf("unknown MCP command %q; available: %v", argv[0], r.Names())
	}
	directory, err := os.Getwd()
	if err != nil {
		return err
	}
	_, err = r.Run(ctx, argv, &coretool.Execution{Dir: directory, Stdout: output, Stderr: diagnostics})
	return err
}
