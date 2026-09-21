package main

import (
	"context"
	"time"

	"github.com/chainreactors/cyber/pkg/cli/configuration"
	cfg "github.com/chainreactors/cyber/pkg/config"
	ioaclient "github.com/chainreactors/cyber/pkg/exts/ioa/client"
)

// Product connections are contributed here; the shared commands do not import
// scanner, search, or collaboration implementations.
func configChecks(ctx context.Context, option *cfg.Option, online bool) []configuration.Check {
	if !online {
		return nil
	}
	value, err := cfg.DistributeFromOption(option)
	if err != nil {
		return []configuration.Check{{Name: "connections", Message: "cannot resolve connection settings"}}
	}
	var selected []string
	if option.CyberhubURL != "" {
		selected = append(selected, "cyberhub")
	}
	if option.FofaKey != "" || option.HunterAPIKey != "" {
		selected = append(selected, "recon")
	}
	if option.TavilyKey != "" || option.SearchConfig.TavilyKeys != "" {
		selected = append(selected, "search")
	}
	if client, err := ioaclient.ReadOptions(option); err == nil && client.URL != "" {
		selected = append(selected, ioaclient.ConfigKey)
	}
	var checks []configuration.Check
	for _, section := range selected {
		checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		results, err := defaultSections().TestConnection(checkCtx, section, value, value)
		cancel()
		if err != nil {
			checks = append(checks, configuration.Check{Name: section, Message: "connection check failed"})
			continue
		}
		for _, result := range results {
			message := "connection successful"
			if !result.Ok {
				message = "connection failed; check endpoint and credentials"
			}
			checks = append(checks, configuration.Check{Name: result.Name, OK: result.Ok, Message: message})
		}
	}
	return checks
}
