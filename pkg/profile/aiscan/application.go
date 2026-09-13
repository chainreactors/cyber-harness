package aiscan

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/provider"
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/events"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/core/telemetry"
	app "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/commands"
	commandext "github.com/chainreactors/aiscan/pkg/exts/commands"
	fileext "github.com/chainreactors/aiscan/pkg/exts/files"
	searchext "github.com/chainreactors/aiscan/pkg/exts/search"
	terminalext "github.com/chainreactors/aiscan/pkg/exts/terminal"
	toolsext "github.com/chainreactors/aiscan/pkg/exts/tools"
	"github.com/chainreactors/aiscan/pkg/toolset"
	arsenal "github.com/chainreactors/aiscan/tools/arsenal"
	"github.com/chainreactors/aiscan/tools/files"
	looptool "github.com/chainreactors/aiscan/tools/loop"
	proxytool "github.com/chainreactors/aiscan/tools/proxy"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

// applicationAssembly is construction-time state owned only by this profile.
// It never survives as a second lifecycle graph; entries are handed directly
// to the profile's single extension.Set.
type applicationAssembly struct {
	application *app.App
	resource    *app.Resource
	commands    *commands.Registry
	tools       *toolset.Registry
	entries     []extension.Entry
}

func newApplicationAssembly(config app.Config, registry *hooks.Registry, stream *events.Stream, proxy *proxytool.ProxyHub, loop agent.Loop, workDir string) (*applicationAssembly, error) {
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

	var scanner *scannerExtension
	if !config.SkipEngines {
		scanner = newScannerExtension(commandRegistry, config, loop, workDir, proxyURL, config.Logger)
	}
	var scannerHandle app.Scanner
	if scanner != nil {
		scannerHandle = scanner
	}
	applicationResource := app.New(config, app.Dependencies{
		Hooks: registry, Events: stream, Commands: commandRegistry, Tools: toolRegistry,
		Bash: bash, Scanner: scannerHandle,
	})
	application := applicationResource.App
	if scanner != nil {
		scanner.application = application
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
		subagentContribution, err := toolsext.New(toolRegistry, subagent)
		if err != nil {
			return nil, err
		}
		loopContribution, err := commandext.New(commandRegistry, "loop", looptool.NewCommand())
		if err != nil {
			return nil, err
		}
		entries = append(entries,
			extension.Entry{ID: "subagent", Extension: subagentContribution},
			extension.Entry{ID: "loop.commands", Extension: loopContribution},
		)
	}
	if plan.Has("proxy") {
		values := proxytool.NewCommands(commandRegistry.Run, proxy, config.Scanner.Proxy)
		contribution, err := commandext.New(commandRegistry, "proxy", values...)
		if err != nil {
			return nil, err
		}
		entries = append(entries, extension.Entry{ID: "proxy.commands", Extension: contribution})
	}
	if plan.Has("arsenal") {
		value, err := arsenal.NewCommand()
		if err != nil {
			application.Logger().Warnf("arsenal init: %v", err)
		} else {
			contribution, err := commandext.New(commandRegistry, "arsenal", value)
			if err != nil {
				return nil, err
			}
			entries = append(entries, extension.Entry{ID: "arsenal.commands", Extension: contribution})
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
	return &applicationAssembly{application: application, resource: applicationResource, commands: commandRegistry, tools: toolRegistry, entries: entries}, nil
}

func (a *applicationAssembly) graph(id string, dependencies ...string) ([]extension.Entry, string) {
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

type scannerExtension struct {
	mu          sync.Mutex
	commands    *commands.Registry
	application *app.App
	appConfig   app.Config
	loop        agent.Loop
	workDir     string
	proxyURL    string
	logger      telemetry.Logger
	engines     *engine.Set
	ready       chan struct{}
	readyOnce   sync.Once
	err         error
	initialized bool
}

func newScannerExtension(commands *commands.Registry, config app.Config, loop agent.Loop, workDir, proxyURL string, logger telemetry.Logger) *scannerExtension {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	return &scannerExtension{commands: commands, appConfig: config, loop: loop, workDir: workDir, proxyURL: proxyURL, logger: logger, ready: make(chan struct{})}
}

func (e *scannerExtension) Load(scope *extension.Scope) (err error) {
	if e == nil || e.application == nil || e.commands == nil || scope == nil {
		return fmt.Errorf("scanner extension is not configured")
	}
	defer func() {
		e.mu.Lock()
		e.err = err
		e.mu.Unlock()
		e.readyOnce.Do(func() { close(e.ready) })
	}()
	e.engines = initEngines(scope.Init(), e.appConfig.Scanner, e.logger)
	e.mu.Lock()
	e.initialized = e.engines != nil
	e.mu.Unlock()
	values, err := buildScannerCommands(e.application, e.engines, e.appConfig, e.loop, e.workDir, e.proxyURL, e.logger)
	if err != nil || len(values) == 0 {
		return err
	}
	return e.commands.Register(scope, "scanner", values...)
}

func (e *scannerExtension) Close(context.Context) error {
	if e == nil {
		return nil
	}
	if e.engines != nil {
		e.engines.Close()
		e.engines = nil
	}
	e.mu.Lock()
	e.initialized = false
	e.mu.Unlock()
	return nil
}

func (e *scannerExtension) Wait(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-e.ready:
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *scannerExtension) State() string {
	select {
	case <-e.ready:
		e.mu.Lock()
		initialized := e.initialized
		e.mu.Unlock()
		if !initialized {
			return "failed"
		}
	default:
		return "loading"
	}
	if len(e.commands.GroupNames("scanner")) > 0 {
		return "ready"
	}
	return "degraded"
}

func initEngines(ctx context.Context, config app.ScannerConfig, logger telemetry.Logger) *engine.Set {
	engines, err := engine.InitWithOptions(ctx, resources.Options{
		CyberhubURL: config.CyberhubURL,
		APIKey:      config.CyberhubKey,
		Mode:        config.CyberhubMode,
		Proxy:       config.Proxy,
	}, logger)
	if err != nil {
		logger.Warnf("scanner engines init error=%q action=continue_without_scanners", err)
		return nil
	}
	engines.SetupUncover(engine.ReconOptions{
		FofaKey:      config.FofaKey,
		HunterAPIKey: config.HunterAPIKey,
		IngressProxy: config.ReconProxy,
		Limit:        config.ReconLimit,
		Credentials:  config.UncoverCredentials,
	}, logger)
	return engines
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

var _ extension.Extension = (*scannerExtension)(nil)
var _ app.Scanner = (*scannerExtension)(nil)
