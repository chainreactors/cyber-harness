package main

import (
	"github.com/chainreactors/cyber/core/extension"
	katanaext "github.com/chainreactors/cyber/exts/katana"
	passiveext "github.com/chainreactors/cyber/exts/passive"
	"github.com/chainreactors/cyber/tools/katana"
	"github.com/chainreactors/cyber/tools/passive"
)

func discoveryExtensions(config appConfig) ([]extension.Extension, *passiveext.Extension) {
	feature := passiveext.New(passive.Config{
		HunterAPIKey: config.Recon.HunterAPIKey,
		Proxy:        config.Recon.Proxy,
		Limit:        intValue(config.Recon.Limit),
		Credentials:  config.ReconCredentials,
	})
	return []extension.Extension{katanaext.New(), feature}, feature
}

func discoveryNames() []string { return []string{"katana", "passive"} }

func discoveryUsage(name string) (string, bool) {
	switch name {
	case "katana":
		return katana.Usage(), true
	case "passive":
		return passive.Usage(), true
	}
	return "", false
}

func discoveryDescription(name string) string {
	switch name {
	case "katana":
		return "Run katana web crawler"
	case "passive":
		return "Run passive cyberspace recon"
	}
	return ""
}

func intValue(value *int) int {
	if value != nil {
		return *value
	}
	return 0
}
