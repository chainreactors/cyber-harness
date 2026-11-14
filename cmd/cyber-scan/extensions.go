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
	guardrailext "github.com/chainreactors/cyber/exts/guardrail"
	okfext "github.com/chainreactors/cyber/exts/okf"
	protonext "github.com/chainreactors/cyber/exts/proton"
	scannerext "github.com/chainreactors/cyber/exts/scanner"
	searchext "github.com/chainreactors/cyber/exts/search"
	subagentext "github.com/chainreactors/cyber/exts/subagent"
	terminalext "github.com/chainreactors/cyber/exts/terminal"
	cfg "github.com/chainreactors/cyber/pkg/config"
	harness "github.com/chainreactors/cyber/pkg/harness"
	"github.com/chainreactors/cyber/tools/katana"
	"github.com/chainreactors/cyber/tools/scan"
)

// extensions returns the product's extensions in the order they must load. It
// threads no capabilities: every extension borrows what it needs from the ones
// ahead of it, so this reads as a membership decision and nothing else.
func extensions(config appConfig, loop agent.Loop, workDir string, proxy extension.Extension, guardrailConfig guardrailext.Config) ([]extension.Extension, error) {
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
	// The loop installation publishes agent.Loop, so it precedes every
	// extension that runs against one.
	extensions = append(extensions, okfext.New(prompt.MainSystem, scan.ScannerSystemTarget), loopext.New(loop), subagentext.New(), arsenal)

	if !config.SkipEngines {
		config.Scanner.Crawler = katana.Crawl
		extensions = append(extensions, scannerext.New(config.Scanner, workDir), protonext.New(protonext.Config{Directory: workDir}))

		extensions = append(extensions, discoveryExtensions(config)...)
	}
	if optionalToolEnabled(config.OptionalTools, "search") {
		extensions = append(extensions, searchext.New(searchext.Options{TavilyKeys: config.TavilyKeys}))
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
