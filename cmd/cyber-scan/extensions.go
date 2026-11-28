package main

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/core/extension"
	loopext "github.com/chainreactors/cyber/exts/agent"
	arsenalext "github.com/chainreactors/cyber/exts/arsenal"
	"github.com/chainreactors/cyber/exts/gateway"
	guardrailext "github.com/chainreactors/cyber/exts/guardrail"
	okfext "github.com/chainreactors/cyber/exts/okf"
	protonext "github.com/chainreactors/cyber/exts/proton"
	proxyext "github.com/chainreactors/cyber/exts/proxy"
	relayext "github.com/chainreactors/cyber/exts/relay"
	scannerext "github.com/chainreactors/cyber/exts/scanner"
	searchext "github.com/chainreactors/cyber/exts/search"
	subagentext "github.com/chainreactors/cyber/exts/subagent"
	terminalext "github.com/chainreactors/cyber/exts/terminal"
	cfg "github.com/chainreactors/cyber/pkg/config"
	harness "github.com/chainreactors/cyber/pkg/harness"
	"github.com/chainreactors/cyber/tools/katana"
	"github.com/chainreactors/cyber/tools/scan"
)

// extensions returns the product's extensions in load order. Gateway binds
// configuration to the existing owners before their consumers are loaded.
func extensions(config appConfig, loop agent.Loop, workDir string, proxy *proxyext.Extension, guardrailConfig guardrailext.Config) ([]extension.Extension, error) {
	config.DataDir = cfg.ResolveDataDir(config.DataDir)
	config.Scanner.Resources.CacheDir = filepath.Join(config.DataDir, "cache")

	bundle, err := EmbeddedBundle()
	if err != nil {
		return nil, err
	}
	arsenal, err := arsenalext.New(filepath.Join(config.DataDir, "arsenal"), ToolSpec.ManagerOption(bundle))
	if err != nil {
		return nil, err
	}
	childEnv := map[string]string{"PATH": arsenal.BinPath() + string(os.PathListSeparator) + os.Getenv("PATH")}

	extensions, err := harness.BaseExtensions(harness.BaseConfig{
		Guardrail:  guardrailConfig,
		Directory:  workDir,
		SkillPaths: config.CLISkillPaths,
		Terminal:   terminalext.Config{Environment: childEnv},
		Provider:   config.Provider,
		Logger:     config.Logger,
		Egress:     proxy,
	})
	if err != nil {
		return nil, err
	}
	discovery, passive := discoveryExtensions(config)
	search := searchext.New(searchext.Options{TavilyKeys: config.TavilyKeys})
	// Relay is a capability of its own: gateway creates its client, and the
	// extension after gateway exposes it only when gateway.relay is set.
	relay := relayext.New()
	bindings := append(passive.Gateway(config.Recon.Service), proxy.Gateway(config.ProxyService), relay.Gateway())
	bindings = append(bindings, search.Gateway()...)
	extensions = append(extensions, gateway.New(config.Gateway, bindings...), relay)
	// The loop installation publishes agent.Loop before its consumers.
	extensions = append(extensions, okfext.New(prompt.MainSystem, scan.ScannerSystemTarget), loopext.New(loop), subagentext.New(), arsenal)

	if !config.SkipEngines {
		config.Scanner.Crawler = katana.Crawl
		extensions = append(extensions, scannerext.New(config.Scanner, workDir), protonext.New(protonext.Config{Directory: workDir}))

		extensions = append(extensions, discovery...)
	}
	if optionalToolEnabled(config.OptionalTools, "search") {
		extensions = append(extensions, search)
	}
	browser, err := browserExtension(config, workDir)
	if err != nil {
		return nil, err
	}
	if browser != nil {
		extensions = append(extensions, browser)
	}
	record, err := recordExtension(config, workDir)
	if err != nil {
		return nil, err
	}
	if record != nil {
		extensions = append(extensions, record)
	}
	return extensions, nil
}

func optionalToolEnabled(selected []string, name string) bool {
	return len(selected) == 0 || slices.Contains(selected, name)
}
