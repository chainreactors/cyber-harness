package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/capability"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	arsenalext "github.com/chainreactors/cyber/pkg/exts/arsenal"
	fileext "github.com/chainreactors/cyber/pkg/exts/files"
	harnessext "github.com/chainreactors/cyber/pkg/exts/harness"
	providerext "github.com/chainreactors/cyber/pkg/exts/provider"
	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
	searchext "github.com/chainreactors/cyber/pkg/exts/search"
	skillsext "github.com/chainreactors/cyber/pkg/exts/skills"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	"github.com/chainreactors/cyber/pkg/toolset"
	"github.com/chainreactors/cyber/tools/files"
	looptool "github.com/chainreactors/cyber/tools/loop"
	proxytool "github.com/chainreactors/cyber/tools/proxy"
	"os"
	"path/filepath"
)

// applicationGraph declares the App-owned portion of the Cyber product graph.
// It owns no lifecycle state; its entries are merged into the command's single
// extension.Set.
type applicationGraph struct {
	application *app.App
	resource    *app.Resource
	harness     *harnessext.Extension
	commands    commands.Runtime
	tools       toolset.Runtime
	entries     []extension.Entry
}

func newApplicationGraph(config app.Config, registry *hooks.Registry, stream *events.Stream, proxy *proxytool.ProxyHub, loop agent.Loop, workDir string) (*applicationGraph, error) {
	config.DataDir = cfg.ResolveDataDir(config.DataDir)
	harness, err := harnessext.New(registry)
	if err != nil {
		return nil, err
	}
	commandRegistry := harness.Commands()
	toolRegistry := harness.ToolRegistry()
	plan := config.Capabilities.Select(capability.Options{
		Groups:        linkedBaseGroups(config.Capabilities),
		OptionalTools: config.Tools.OptionalTools,
	})
	proxyURL := config.Scanner.Proxy
	var proxyCA string
	var egress func(context.Context) (string, string, func())
	if proxy != nil {
		egress = proxy.Egress
		if proxy.ProxyURL() != "" {
			proxyURL, proxyCA = proxy.ProxyURL(), proxy.CAPath()
		}
	}

	var entries []extension.Entry
	var childEnv map[string]string
	if plan.Has("arsenal") {
		a, err := arsenalext.New(filepath.Join(cfg.ResolveDataDir(config.DataDir), "arsenal"), commandRegistry)
		if err != nil {
			return nil, err
		}
		entries = append(entries, extension.Entry{ID: "arsenal", Extension: a})
		childEnv = map[string]string{"PATH": a.BinDir() + string(os.PathListSeparator) + os.Getenv("PATH")}
	}
	var bash *commands.BashTool
	if plan.Has("core") {
		workspace, err := fileext.New(toolRegistry, registry, files.Config{Directory: workDir})
		if err != nil {
			return nil, err
		}
		terminal, err := terminalext.New(registry, toolRegistry, commandRegistry, terminalext.Config{
			Directory: workDir, Timeout: config.Tools.BashTimeout, Environment: childEnv,
			Proxy: proxyURL, ProxyCA: proxyCA, Egress: egress,
		})
		if err != nil {
			return nil, err
		}
		bash = terminal.Bash()
		entries = append(entries,
			extension.Entry{ID: "files", Extension: workspace},
			extension.Entry{ID: "terminal", Extension: terminal},
		)
	}

	var application *app.App
	var scanner *scannerext.Extension
	if !config.SkipEngines {
		scanner = scannerext.New(func() *app.App { return application }, commandRegistry, scannerext.Config{DataDir: config.DataDir, Scanner: config.Scanner, Capabilities: config.Capabilities}, loop, workDir, proxyURL, config.Logger)
	}
	var scannerHandle app.Scanner
	if scanner != nil {
		scannerHandle = scanner
	}
	skillResource, err := skillsext.NewLibrary(skillsext.LibraryConfig{Directory: workDir, Paths: config.CLISkillPaths, Catalog: config.Capabilities, Bundles: config.SkillBundles})
	if err != nil {
		return nil, err
	}
	entries = append(entries, extension.Entry{ID: "skills", Extension: skillResource})
	services := extension.NewServices()
	for _, contribution := range []struct {
		key   extension.Service
		value any
	}{
		{app.HooksService, registry}, {app.EventsService, stream},
		{app.CommandsService, commandRegistry}, {app.ToolsService, toolRegistry},
		{app.SkillsService, skillResource.Store()},
	} {
		if err := services.Provide(contribution.key, contribution.value); err != nil {
			return nil, err
		}
	}
	if scannerHandle != nil {
		if err := services.Provide(app.ScannerService, scannerHandle); err != nil {
			return nil, err
		}
	}
	if bash != nil {
		if err := services.Provide(app.BashService, bash); err != nil {
			return nil, err
		}
	}
	services.Seal()
	appServices, err := app.Services(services)
	if err != nil {
		return nil, err
	}
	applicationResource, err := app.New(config, appServices)
	if err != nil {
		return nil, err
	}
	application = applicationResource.App
	if config.Provider.Enabled {
		resource, err := providerext.New(&application.Providers, config.Provider, application.Logger())
		if err != nil {
			return nil, err
		}
		entries = append(entries, extension.Entry{ID: "provider", Extension: resource})
	}

	if plan.Has("core") {
		subagent := agent.NewSubAgentTool(func(name string) (agent.AgentType, error) {
			if application.Skills == nil {
				return agent.AgentType{}, fmt.Errorf("agent type %q not found", name)
			}
			skill, ok := application.Skills.ByName(name)
			if !ok {
				return agent.AgentType{}, fmt.Errorf("agent type %q not found", name)
			}
			if !skill.Agent {
				return agent.AgentType{}, fmt.Errorf("skill %q is not configured as an agent type", name)
			}
			return agent.AgentType{
				FormattedPrompt: application.Skills.FormatInvocation(skill, ""),
				Model:           skill.AgentModel,
				Background:      skill.AgentBackground,
			}, nil
		})
		if err := toolRegistry.Register("agent", subagent); err != nil {
			return nil, err
		}
		if err := commandRegistry.Register("loop", "loop", looptool.NewCommand()); err != nil {
			return nil, err
		}
	}
	if plan.Has("proxy") {
		values := proxytool.NewCommands(commandRegistry.Run, proxy, config.Scanner.Proxy)
		if err := commandRegistry.Register("proxy", "proxy", values...); err != nil {
			return nil, err
		}
	}
	if plan.Has("search") {
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
		var dependencies []string
		if scanner != nil {
			searchConfig.ResolveIndex = scanner.Index
			dependencies = append(dependencies, "scanner")
		}
		search, err := searchext.New(toolRegistry, commandRegistry, searchConfig)
		if err != nil {
			return nil, err
		}
		entries = append(entries, extension.Entry{ID: "search", DependsOn: dependencies, Extension: search})
	}
	if scanner != nil {
		var dependencies []string
		if config.Provider.Enabled {
			dependencies = append(dependencies, "provider")
		}
		entries = append(entries, extension.Entry{ID: "scanner", DependsOn: dependencies, Extension: scanner})
	}
	editionEntries, err := editionExtensionEntries(application, toolRegistry, commandRegistry, config, plan, workDir)
	if err != nil {
		return nil, err
	}
	entries = append(entries, editionEntries...)
	return &applicationGraph{application: application, resource: applicationResource, harness: harness, commands: commandRegistry, tools: toolRegistry, entries: entries}, nil
}

func (a *applicationGraph) entriesFor(id string, dependencies ...string) ([]extension.Entry, string) {
	entries := []extension.Entry{{ID: id, DependsOn: append([]string(nil), dependencies...), Extension: a.resource}}
	idMap := make(map[string]string, len(a.entries))
	for _, entry := range a.entries {
		idMap[entry.ID] = id + "." + entry.ID
	}
	contributors := make([]string, 0, len(a.entries))
	for _, entry := range a.entries {
		entry.ID = idMap[entry.ID]
		mapped := []string{id}
		for _, dependency := range entry.DependsOn {
			if replacement, exists := idMap[dependency]; exists {
				dependency = replacement
			}
			mapped = append(mapped, dependency)
		}
		entry.DependsOn = mapped
		entries = append(entries, entry)
		contributors = append(contributors, entry.ID)
	}
	harnessID := id + ".harness"
	entries = append(entries, extension.Entry{ID: harnessID, DependsOn: append([]string{id}, contributors...), Extension: a.harness})
	return entries, harnessID
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

func linkedBaseGroups(catalog capability.Catalog) []string {
	seen := make(map[string]bool)
	var groups []string
	for _, descriptor := range catalog.All() {
		baseService := descriptor.Kind == capability.KindService
		if (descriptor.Kind != capability.KindTool && !baseService) || descriptor.Group == "" || seen[descriptor.Group] {
			continue
		}
		seen[descriptor.Group] = true
		groups = append(groups, descriptor.Group)
	}
	return groups
}
