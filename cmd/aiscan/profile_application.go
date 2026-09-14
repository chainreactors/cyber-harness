package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/provider"
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/events"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	app "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/commands"
	fileext "github.com/chainreactors/aiscan/pkg/exts/files"
	scannerext "github.com/chainreactors/aiscan/pkg/exts/scanner"
	searchext "github.com/chainreactors/aiscan/pkg/exts/search"
	terminalext "github.com/chainreactors/aiscan/pkg/exts/terminal"
	"github.com/chainreactors/aiscan/pkg/toolset"
	arsenal "github.com/chainreactors/aiscan/tools/arsenal"
	"github.com/chainreactors/aiscan/tools/files"
	looptool "github.com/chainreactors/aiscan/tools/loop"
	proxytool "github.com/chainreactors/aiscan/tools/proxy"
)

// applicationGraph declares the App-owned portion of the AIScan product graph.
// It owns no lifecycle state; its entries are merged into the command's single
// extension.Set.
type applicationGraph struct {
	application *app.App
	resource    *app.Resource
	commands    *commands.Registry
	tools       *toolset.Registry
	entries     []extension.Entry
}

func newApplicationGraph(config app.Config, registry *hooks.Registry, stream *events.Stream, proxy *proxytool.ProxyHub, loop agent.Loop, workDir string) (*applicationGraph, error) {
	commandRegistry := commands.NewRegistry(registry)
	toolRegistry := toolset.NewRegistry(registry)
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
	var bash *commands.BashTool
	if plan.Has("core") {
		workspace, err := fileext.New(toolRegistry, registry, files.Config{Directory: workDir})
		if err != nil {
			return nil, err
		}
		terminal, err := terminalext.New(registry, toolRegistry, commandRegistry, terminalext.Config{
			Directory: workDir, Timeout: config.Tools.BashTimeout,
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
		scanner = scannerext.New(func() *app.App { return application }, commandRegistry, config, loop, workDir, proxyURL, config.Logger)
	}
	var scannerHandle app.Scanner
	if scanner != nil {
		scannerHandle = scanner
	}
	applicationResource, err := app.New(config, app.Dependencies{
		Hooks: registry, Events: stream, Commands: commandRegistry, Tools: toolRegistry,
		Bash: bash, Scanner: scannerHandle,
	})
	if err != nil {
		return nil, err
	}
	application = applicationResource.App

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
	if plan.Has("arsenal") {
		value, err := arsenal.NewCommand()
		if err != nil {
			application.Logger().Warnf("arsenal init: %v", err)
		} else {
			if err := commandRegistry.Register("arsenal", "arsenal", value); err != nil {
				return nil, err
			}
		}
	}
	if plan.Has("search") {
		search, err := searchext.New(toolRegistry, commandRegistry, searchext.Config{
			Search: func(ctx context.Context, query string, maxResults int) (string, error) {
				search := providerWebSearch(application)
				if search == nil {
					return "", fmt.Errorf("provider web search is unavailable")
				}
				return search(ctx, query, maxResults)
			},
			TavilyKeys: config.Tools.TavilyKeys,
			Proxy:      proxy,
		})
		if err != nil {
			return nil, err
		}
		entries = append(entries, extension.Entry{ID: "search", Extension: search})
	}
	if scanner != nil {
		entries = append(entries, extension.Entry{ID: "scanner", Extension: scanner})
	}
	editionEntries, err := editionExtensionEntries(application, toolRegistry, commandRegistry, config, plan, workDir)
	if err != nil {
		return nil, err
	}
	entries = append(entries, editionEntries...)
	return &applicationGraph{application: application, resource: applicationResource, commands: commandRegistry, tools: toolRegistry, entries: entries}, nil
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
	commandRegistryID := id + ".command-registry"
	toolRegistryID := id + ".tool-registry"
	entries = append(entries,
		extension.Entry{ID: commandRegistryID, DependsOn: append([]string{id}, contributors...), Extension: a.commands},
		extension.Entry{ID: toolRegistryID, DependsOn: append(append([]string(nil), contributors...), commandRegistryID), Extension: a.tools},
	)
	return entries, toolRegistryID
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
