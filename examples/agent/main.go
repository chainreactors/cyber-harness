package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/runner"
	transportpkg "github.com/chainreactors/aiscan/pkg/transport"
	goflags "github.com/jessevdk/go-flags"
)

func main() {
	var option cfg.Option
	parser := goflags.NewParser(&option, goflags.Default&^goflags.PrintErrors)
	parser.Usage = `[OPTIONS]

AIScan agent example - reference wiring for the root agent packages

Run manually:
  go run ./examples/agent -p "list available tools using arsenal"
  go run ./examples/agent -p "install nuclei and scan target" -i http://target.com
  go run ./examples/agent --base-url https://api.deepseek.com --model deepseek-v4-pro`

	if _, err := parser.Parse(); err != nil {
		if flagsErr, ok := err.(*goflags.Error); ok && flagsErr.Type == goflags.ErrHelp {
			parser.WriteHelp(os.Stdout)
			return
		}
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}

	if option.Version {
		fmt.Printf("AIScan agent example v%s\n", cfg.Version)
		return
	}

	cfgPath, err := runner.ResolveRuntimeConfig(&option)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
	if cfgPath != "" {
		option.ConfigFile = cfgPath
		if option.Debug {
			fmt.Fprintf(os.Stderr, "loaded config: %s\n", cfgPath)
		}
	}

	logger := telemetry.GlobalLogger(telemetry.LogConfig{
		Debug: option.Debug, Quiet: option.Quiet, Output: os.Stderr, Color: !option.NoColor,
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(option.Timeout)*time.Second)
	defer cancel()

	var interruptMu sync.RWMutex
	var interruptFn func() bool
	sigChan := make(chan os.Signal, 2)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)
	go func() {
		for sig := range sigChan {
			interruptMu.RLock()
			fn := interruptFn
			interruptMu.RUnlock()
			if sig == os.Interrupt && fn != nil && fn() {
				continue
			}
			cancel()
			return
		}
	}()

	err = transportpkg.Run(ctx, &option, logger, os.Stdin, os.Stdout, func(fn func() bool) {
		interruptMu.Lock()
		interruptFn = fn
		interruptMu.Unlock()
	})
	if err != nil {
		logger.Errorf("agent example failed: %s", err)
		os.Exit(1)
	}
}
