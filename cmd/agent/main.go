package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/runner"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/webagent"
	goflags "github.com/jessevdk/go-flags"
)

func main() {
	cfg.ScannerEnabled = false

	var option cfg.Option
	parser := goflags.NewParser(&option, goflags.Default&^goflags.PrintErrors)
	parser.Usage = `[OPTIONS]

aiscan-agent - Minimal AI agent with Arsenal toolkit

Examples:
  aiscan-agent -p "list available tools using arsenal"
  aiscan-agent -p "install nuclei and scan target" -i http://target.com
  aiscan-agent --base-url https://api.deepseek.com --model deepseek-v4-pro`

	if _, err := parser.Parse(); err != nil {
		if flagsErr, ok := err.(*goflags.Error); ok && flagsErr.Type == goflags.ErrHelp {
			parser.WriteHelp(os.Stdout)
			return
		}
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}

	if option.Version {
		fmt.Printf("aiscan-agent v%s\n", cfg.Version)
		return
	}

	cfgPath, err := cfg.ResolveRuntimeConfig(&option)
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

	sigChan := make(chan os.Signal, 2)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Fprintf(os.Stderr, "\nPress Ctrl+C again to exit\n")
		cancel()
		<-sigChan
		os.Exit(1)
	}()

	if option.WebURL != "" {
		err = webagent.Run(ctx, &option, logger)
	} else {
		err = runner.RunAgentMode(ctx, &option, logger)
	}
	if err != nil {
		logger.Errorf("agent failed: %s", err)
		os.Exit(1)
	}
}
