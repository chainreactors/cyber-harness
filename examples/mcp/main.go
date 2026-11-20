// A no-model MCP-to-CLI host, using the same command registry as AI bash.
package main

import (
	"context"
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
	mcptools "github.com/chainreactors/cyber/tools/mcp"
)

func main() {
	configPath := flag.String("config", "", "MCP JSON server map")
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
		return fmt.Errorf("usage: go run ./examples/mcp -config mcp.json [ida --list | ida <tool> --<parameter> <value>]")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	config, err := mcptools.Parse(data)
	if err != nil {
		return err
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
