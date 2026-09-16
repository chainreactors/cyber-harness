package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/inbox"
	agentprompt "github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/aop"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/telemetry"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	ioaext "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	ioaconsole "github.com/chainreactors/cyber/pkg/exts/ioa/client/console"
	observeext "github.com/chainreactors/cyber/pkg/exts/observe"
	proxyext "github.com/chainreactors/cyber/pkg/exts/proxy"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	sessionconsole "github.com/chainreactors/cyber/pkg/exts/session/console"
	telemetryext "github.com/chainreactors/cyber/pkg/exts/telemetry"
	tuiext "github.com/chainreactors/cyber/pkg/exts/tui"
	"github.com/chainreactors/cyber/pkg/namespaces"
	nodepkg "github.com/chainreactors/cyber/pkg/node"
	profilepkg "github.com/chainreactors/cyber/pkg/profile"
	managementapi "github.com/chainreactors/cyber/pkg/web/api"
	"github.com/chainreactors/cyber/skills"
	ioatools "github.com/chainreactors/cyber/tools/ioa"
)

type cyberProfileConfig struct {
	Option      *cfg.Option
	Application applicationConfig
	IOA         *ioatools.Config
	// Runtime nil creates the application-only profile used by the Web service.
	Runtime   *agentsession.Config
	Observe   []observeext.Kind
	Output    string
	Artifacts managementapi.ArtifactImporter
}

func profileConfigFromOption(option *cfg.Option, providerMode profilepkg.ProviderMode, runtimeConfig *agentsession.Config, logger telemetry.Logger) (cyberProfileConfig, error) {
	ioaConfig, err := ioaext.ConfigFromOption(option)
	if err != nil {
		return cyberProfileConfig{}, err
	}
	application := applicationConfigFromOption(option, providerMode, logger)
	return cyberProfileConfig{
		Option: option, Application: application,
		IOA: ioaConfig, Runtime: cloneConfig(runtimeConfig),
		Observe: parseObserve(option.Observe), Output: resolveOutputPath(option),
	}, nil
}

func resolveOutputPath(option *cfg.Option) string {
	if option == nil {
		return ""
	}
	return strings.TrimSpace(option.OutputFile)
}

func parseObserve(value string) []observeext.Kind {
	var result []observeext.Kind
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, observeext.Kind(item))
		}
	}
	return result
}

type cyberProfile struct {
	extensions *extension.Set
	app        *apppkg.App
	runtime    *agentsession.Runtime
	ioa        *ioatools.Runtime
	tui        *tuiext.Extension
	namespaces *namespaces.Catalog
}

var _ profilepkg.Application = (*cyberProfile)(nil)

var cyberProfileFactory profilepkg.Factory = func(request profilepkg.Request) (profilepkg.Application, error) {
	if request.Option == nil {
		return nil, fmt.Errorf("cyber profile option is required")
	}
	if request.Option.Resolved == nil {
		resolved, err := productSections(false).ResolveValues(request.Option.Extensions, nil, nil)
		if err != nil {
			return nil, err
		}
		option := *request.Option
		option.Resolved = resolved
		option.Extensions = resolved.Values()
		request.Option = &option
	}
	config, err := profileConfigFromOption(request.Option, request.ProviderMode, request.Runtime, request.Logger)
	if err != nil {
		return nil, err
	}
	return newCyberProfile(config)
}

func newCyberProfile(config cyberProfileConfig) (*cyberProfile, error) {
	product := &cyberProfile{}
	if config.Runtime != nil {
		config.Runtime = cloneConfig(config.Runtime)
		config.Runtime.BaseSkills = append([]string{"cyber"}, config.Runtime.BaseSkills...)
	}
	if config.Option == nil {
		return nil, fmt.Errorf("cyber profile option is required")
	}
	if config.Application.Logger == nil {
		config.Application.Logger = telemetry.NopLogger()
	}
	logger := config.Application.Logger
	config.Runtime = cloneConfig(config.Runtime)
	nodeName := config.Option.NodeName
	if config.IOA != nil && config.IOA.NodeName != "" {
		nodeName = config.IOA.NodeName
	}
	nodeName = cfg.ResolveNodeName(nodeName)
	if config.IOA != nil {
		ioaConfig := *config.IOA
		ioaConfig.NodeName = nodeName
		config.IOA = &ioaConfig
	}

	workDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve Cyber working directory: %w", err)
	}
	hookRegistry := hooks.New()
	capture := config.Application.Tools.MitmCapture == nil || *config.Application.Tools.MitmCapture
	proxyExtension, err := proxyext.New(workDir, config.Application.Scanner.Resources.Proxy, capture, hookRegistry, config.Application.Tools.TrafficStorage)
	if err != nil {
		return nil, fmt.Errorf("construct proxy infrastructure: %w", err)
	}
	proxyHub := proxyExtension.Hub()
	eventStream := events.New()
	var runtimeLoop agent.Loop
	if config.Runtime != nil {
		runtimeLoop = config.Runtime.Loop
	}
	scannerLoop := runtimeLoop
	if scannerLoop == nil && config.Application.Provider.Mode != provider.StartupDisabled {
		scannerLoop = agent.StandardLoop{}
	}
	applicationGraph, err := newApplicationGraph(config.Application, hookRegistry, eventStream, proxyHub, scannerLoop, workDir)
	if err != nil {
		return nil, fmt.Errorf("construct Cyber application: %w", err)
	}
	application := applicationGraph.application

	namespaceCatalog := namespaces.New()
	values := []extension.Extension{namespaceCatalog}
	if strings.TrimSpace(config.Output) != "" {
		output, outputErr := telemetryext.New(eventStream, telemetryext.Options{Path: config.Output})
		if outputErr != nil {
			return nil, outputErr
		}
		values = append(values, output)
	}
	if config.Artifacts != nil {
		projection, projectionErr := newArtifactProjection(eventStream, config.Artifacts, logger)
		if projectionErr != nil {
			return nil, projectionErr
		}
		values = append(values, projection)
	}
	var observer *observeext.Extension
	if len(config.Observe) > 0 {
		var observeErr error
		observer, observeErr = observeext.New(hookRegistry, eventStream, observeext.Options{Kinds: config.Observe, Logger: logger})
		if observeErr != nil {
			return nil, observeErr
		}
		values = append(values, observer)
	}
	values = append(values, proxyExtension)
	values = append(values, applicationGraph.extensions...)
	var ioa *ioaext.Extension
	if config.IOA != nil {
		bundle, diagnostics := ioaext.Skills()
		if len(diagnostics) > 0 {
			return nil, fmt.Errorf("load IOA skills: %v", diagnostics)
		}
		deps := ioaext.Dependencies{Events: eventStream, Logger: logger, Skills: []skills.Bundle{bundle}}
		if config.Runtime != nil {
			deps.Deliver = func(ctx context.Context, message inbox.Message) error {
				if product.extensions == nil || !product.extensions.Active() {
					return agentsession.ErrUnavailable
				}
				return product.runtime.Deliver(ctx, message)
			}
		}
		ioa = ioaext.New(*config.IOA, deps)
		values = append(values, ioa)
	}
	var run *agentsession.Runtime
	var ioaRuntime *ioatools.Runtime
	if config.Runtime != nil || ioa != nil {
		product.tui = tuiext.New()
		values = append(values, product.tui)
	}
	if ioa != nil {
		ioaRuntime = ioa.Runtime()
		presentation, err := ioaconsole.New(ioaRuntime, config.IOA.Space, config.IOA.URL)
		if err != nil {
			return nil, err
		}
		values = append(values, presentation)
	}
	if config.Runtime != nil {
		agentConfig := *config.Runtime
		agentConfig.Application = application
		agentConfig.NodeName = nodeName
		if config.IOA != nil && config.IOA.Space != "" {
			agentConfig.Preamble = strings.TrimSpace(agentConfig.Preamble + "\n" + ioaext.Preamble(*config.IOA))
		}

		agentConfig.Option, agentConfig.Logger = config.Option, logger
		if runtimeLoop != nil {
			loopExtension := loopext.New(runtimeLoop)
			agentConfig.Loop = loopExtension.Runtime()
			values = append(values, loopExtension)
		} else {
			agentConfig.Loop = nil
		}
		agentExtension, err := sessionext.New(agentConfig)
		if err != nil {
			return nil, fmt.Errorf("construct Cyber runtime: %w", err)
		}
		run = agentExtension.Runtime()
		values = append(values, agentExtension)
		values = append(values, extension.Func{LoadFunc: func(scope *extension.Scope) error {
			return extension.Add(scope, run.NamespaceBindings()...)
		}})
		presentation, err := sessionconsole.New(run)
		if err != nil {
			return nil, err
		}
		values = append(values, presentation)
	}
	extensions, err := extension.New(values...)
	if err != nil {
		return nil, err
	}
	product.extensions, product.app, product.runtime, product.ioa = extensions, application, run, ioaRuntime
	product.namespaces = namespaceCatalog
	return product, nil
}

func (p *cyberProfile) Load(ctx context.Context) error {
	if p == nil || p.extensions == nil {
		return fmt.Errorf("Cyber profile is required")
	}
	return p.extensions.Load(ctx)
}

func (p *cyberProfile) App() (*apppkg.App, error) {
	if p == nil || p.extensions == nil || !p.extensions.Active() || p.app == nil {
		return nil, fmt.Errorf("Cyber profile is not active")
	}
	return p.app, nil
}

func (p *cyberProfile) Runtime() (*agentsession.Runtime, error) {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return nil, fmt.Errorf("Cyber profile is not active")
	}
	if p.runtime == nil {
		return nil, fmt.Errorf("Cyber profile has no Agent runtime")
	}
	return p.runtime, nil
}

func (p *cyberProfile) RegisterNamespaces(mux *aop.NamespaceMux) error {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return fmt.Errorf("Cyber profile is not active")
	}
	return p.namespaces.Bind(mux)
}

func (p *cyberProfile) Close(ctx context.Context) error {
	if p == nil || p.extensions == nil {
		return nil
	}
	return p.extensions.Close(ctx)
}

func cloneConfig(config *agentsession.Config) *agentsession.Config {
	if config == nil {
		return nil
	}
	cloned := *config
	cloned.BaseSkills = append([]string(nil), config.BaseSkills...)
	if config.PromptConfig != nil {
		prompt := *config.PromptConfig
		prompt.LoadedSkills = append([]agentprompt.LoadedSkill(nil), config.PromptConfig.LoadedSkills...)
		cloned.PromptConfig = &prompt
	}
	return &cloned
}

func (p *cyberProfile) AgentStatus() *aop.AgentStatus {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return &aop.AgentStatus{}
	}
	status := nodepkg.AgentStatus(p.app)
	if p.ioa != nil {
		collaboration := p.ioa.Status()
		status.Bound, status.Space = collaboration.Bound, collaboration.Space
	}
	return status
}

// ConsoleBindings lends the selected presentation contributions.
func (p *cyberProfile) ConsoleBindings() *consoleapi.Bindings {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return nil
	}
	if p.tui == nil {
		return nil
	}
	return p.tui.Bindings()
}
