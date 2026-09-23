package main

import (
	"context"
	"time"

	"github.com/chainreactors/cyber/pkg/cli/configuration"
	cfg "github.com/chainreactors/cyber/pkg/config"
	ioaclient "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
	searchext "github.com/chainreactors/cyber/pkg/exts/search"
)

// Product connections are contributed here; the shared commands do not import
// scanner, search, or collaboration implementations.
func configChecks(ctx context.Context, option *cfg.Option, online bool) []configuration.Check {
	if !online {
		return nil
	}
	value, err := DistributeFromOption(option)
	if err != nil {
		return []configuration.Check{{Name: "connections", Message: "cannot resolve connection settings"}}
	}
	var selected []string
	if hub, err := scannerext.ReadCyberhub(option); err == nil && hub.URL != "" {
		selected = append(selected, scannerext.CyberhubConfigKey)
	}
	recon, reconErr := scannerext.ReadRecon(option)
	if reconErr == nil && (recon.FofaKey != "" || recon.HunterAPIKey != "") {
		selected = append(selected, scannerext.ReconConfigKey)
	}
	searchKeys, searchErr := searchext.ReadKeys(option)
	if (reconErr == nil && recon.TavilyKey != "") || (searchErr == nil && searchKeys != "") {
		selected = append(selected, searchext.ConfigKey)
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
