package main

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/chainreactors/cyber/agent"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/extension"
	base "github.com/chainreactors/cyber/pkg/base"
	arsenalext "github.com/chainreactors/cyber/pkg/exts/arsenal"
	nativeext "github.com/chainreactors/cyber/pkg/exts/native"
	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
	searchext "github.com/chainreactors/cyber/pkg/exts/search"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
)

type appFactory func(appConfig, string) (extension.Extension, error)

var appFactories []appFactory

func registerApp(factory appFactory) {
	appFactories = append(appFactories, factory)
}

// newAppGraph returns the product's extensions in the order they must load. It
// threads no capabilities: every extension borrows what it needs from the ones
// ahead of it, so this reads as a membership decision and nothing else.
func newAppGraph(config appConfig, loop agent.Loop, workDir string, proxy extension.Extension) ([]extension.Extension, error) {
	config.DataDir = cfg.ResolveDataDir(config.DataDir)
	config.Scanner.Resources.CacheDir = filepath.Join(config.DataDir, "cache")

	arsenal, err := arsenalext.New(filepath.Join(config.DataDir, "arsenal"))
	if err != nil {
		return nil, err
	}
	childEnv := map[string]string{"PATH": arsenal.BinDir() + string(os.PathListSeparator) + os.Getenv("PATH")}

	extensions, err := base.New(base.Config{
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
	extensions = append(extensions, arsenal, nativeext.New())

	var scanner *scannerext.Extension
	if !config.SkipEngines {
		scanner = scannerext.New(config.Scanner, loop, workDir, config.Logger)
		extensions = append(extensions, scanner)
	}
	if optionalToolEnabled(config.Tools.OptionalTools, "search") {
		searchConfig := searchext.Config{TavilyKeys: config.Tools.TavilyKeys}
		if scanner != nil {
			searchConfig.ResolveIndex = scanner.Index
		}
		extensions = append(extensions, searchext.New(searchConfig))
	}
	optional, err := appExtensions(config, workDir)
	if err != nil {
		return nil, err
	}
	return append(extensions, optional...), nil
}

func appExtensions(config appConfig, workDir string) ([]extension.Extension, error) {
	result := make([]extension.Extension, 0, len(appFactories))
	for _, factory := range appFactories {
		value, err := factory(config, workDir)
		if err != nil {
			return nil, err
		}
		if value != nil {
			result = append(result, value)
		}
	}
	return result, nil
}

func optionalToolEnabled(selected []string, name string) bool {
	return len(selected) == 0 || slices.Contains(selected, name)
}
