package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	arsenalext "github.com/chainreactors/cyber/pkg/exts/arsenal"
	fileext "github.com/chainreactors/cyber/pkg/exts/files"
	nativeext "github.com/chainreactors/cyber/pkg/exts/native"
	providerext "github.com/chainreactors/cyber/pkg/exts/provider"
	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
	searchext "github.com/chainreactors/cyber/pkg/exts/search"
	skillsext "github.com/chainreactors/cyber/pkg/exts/skills"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	"github.com/chainreactors/cyber/pkg/toolset"
	"github.com/chainreactors/cyber/tools/files"
	proxytool "github.com/chainreactors/cyber/tools/proxy"
)

type appGraph struct {
	application *app.App
	extensions  []extension.Extension
}

type appFactory func(appConfig, string) (extension.Extension, error)

var appFactories []appFactory

func registerApp(factory appFactory) {
	appFactories = append(appFactories, factory)
}

func newAppGraph(config appConfig, registry *hooks.Registry, stream *events.Stream, proxy *proxytool.ProxyHub, loop agent.Loop, workDir string) (*appGraph, error) {
	config.DataDir = cfg.ResolveDataDir(config.DataDir)
	config.Scanner.Resources.CacheDir = filepath.Join(config.DataDir, "cache")
	commandRegistry := commands.NewRegistry(registry)
	toolRegistry := toolset.NewRegistry(registry)
	skillLibrary, err := skillsext.NewLibrary(skillsext.LibraryConfig{Directory: workDir, Paths: config.CLISkillPaths})
	if err != nil {
		return nil, err
	}

	proxyURL := config.Scanner.Resources.Proxy
	var proxyCA string
	var egress func(context.Context) (string, string, func())
	if proxy != nil {
		egress = proxy.Egress
		if proxy.ProxyURL() != "" {
			proxyURL, proxyCA = proxy.ProxyURL(), proxy.CAPath()
		}
	}

	extensions := []extension.Extension{commandRegistry, toolRegistry, skillLibrary}
	arsenal, err := arsenalext.New(filepath.Join(config.DataDir, "arsenal"))
	if err != nil {
		return nil, err
	}
	childEnv := map[string]string{"PATH": arsenal.BinDir() + string(os.PathListSeparator) + os.Getenv("PATH")}

	workspace, err := fileext.New(registry, files.Config{Directory: workDir})
	if err != nil {
		return nil, err
	}
	terminal, err := terminalext.New(registry, commandRegistry, terminalext.Config{
		Directory: workDir, Environment: childEnv,
		Proxy: proxyURL, ProxyCA: proxyCA, Egress: egress,
	})
	if err != nil {
		return nil, err
	}

	application, err := app.New(config.Logger, app.Dependencies{
		Hooks: registry, Events: stream, Commands: commandRegistry, Tools: toolRegistry,
		Skills: skillLibrary.Store(), Bash: terminal.Bash(),
	})
	if err != nil {
		return nil, err
	}
	if config.Provider.Mode != provider.StartupDisabled {
		providerResource, err := providerext.New(&application.Providers, config.Provider, application.Logger())
		if err != nil {
			return nil, err
		}
		extensions = append(extensions, providerResource)
	}
	var scanner *scannerext.Extension
	if !config.SkipEngines {
		scanner = scannerext.New(application, config.Scanner, loop, workDir, proxyURL, config.Logger)
	}
	extensions = append(extensions, arsenal, workspace, terminal)

	native, err := nativeext.New(application, proxy, config.Scanner.Resources.Proxy)
	if err != nil {
		return nil, err
	}
	extensions = append(extensions, native)
	if scanner != nil {
		extensions = append(extensions, scanner)
	}
	if optionalToolEnabled(config.Tools.OptionalTools, "search") {
		searchConfig := searchext.Config{
			Search: func(ctx context.Context, query string, maxResults int) (string, error) {
				search := providerWebSearch(application)
				if search == nil {
					return "", fmt.Errorf("provider web search is unavailable")
				}
				return search(ctx, query, maxResults)
			},
			TavilyKeys: config.Tools.TavilyKeys,
			Proxy:      proxy,
		}
		if scanner != nil {
			searchConfig.ResolveIndex = scanner.Index
		}
		extensions = append(extensions, searchext.New(searchConfig))
	}
	optional, err := appExtensions(config, workDir)
	if err != nil {
		return nil, err
	}
	extensions = append(extensions, optional...)
	return &appGraph{application: application, extensions: extensions}, nil
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

func providerWebSearch(application *app.App) func(context.Context, string, int) (string, error) {
	model, _ := application.ProviderState()
	searcher, ok := model.(provider.WebSearchProvider)
	if !ok {
		return nil
	}
	return func(ctx context.Context, query string, maxResults int) (string, error) {
		response, err := searcher.WebSearch(ctx, query, maxResults)
		if err != nil {
			return "", err
		}
		var text strings.Builder
		fmt.Fprintf(&text, "Web search results for: %s\n\n", query)
		if len(response.Results) == 0 && response.Summary == "" {
			text.WriteString("No results found.\n")
			return text.String(), nil
		}
		for index, result := range response.Results {
			fmt.Fprintf(&text, "[%d] %s\n    URL: %s\n\n", index+1, result.Title, result.URL)
		}
		if response.Summary != "" {
			text.WriteString("Summary:\n")
			text.WriteString(response.Summary)
			text.WriteByte('\n')
		}
		return text.String(), nil
	}
}
