package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/capability"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	"github.com/chainreactors/cyber/pkg/edition"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	ioaext "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	ioaconsole "github.com/chainreactors/cyber/pkg/exts/ioa/client/console"
	observeext "github.com/chainreactors/cyber/pkg/exts/observe"
	proxyext "github.com/chainreactors/cyber/pkg/exts/proxy"
	agentext "github.com/chainreactors/cyber/pkg/exts/session"
	sessionconsole "github.com/chainreactors/cyber/pkg/exts/session/console"
	signalsext "github.com/chainreactors/cyber/pkg/exts/signals"
	telemetryext "github.com/chainreactors/cyber/pkg/exts/telemetry"
	tuiext "github.com/chainreactors/cyber/pkg/exts/tui"
	profilepkg "github.com/chainreactors/cyber/pkg/profile"
	managementapi "github.com/chainreactors/cyber/pkg/web/api"
	"github.com/chainreactors/cyber/skills"
	ioatools "github.com/chainreactors/cyber/tools/ioa"
	proxytool "github.com/chainreactors/cyber/tools/proxy"
)

const (
	telemetryID   = "cyber.telemetry"
	artifactsID   = "cyber.artifacts"
	observeID     = "cyber.observe"
	proxyID       = "cyber.proxy"
	applicationID = "cyber.application"
	ioaID         = "cyber.ioa-client"
	agentID       = "cyber.agent"
)

type cyberProfileConfig struct {
	Option      *cfg.Option
	Application apppkg.Config
	IOA         *ioatools.Config
	// Runtime nil creates the application-only profile used by the Web service.
	Runtime   *agentext.Config
	Logger    telemetry.Logger
	Observe   []observeext.Kind
	Output    string
	Artifacts managementapi.ArtifactImporter
}

func profileConfigFromOption(option *cfg.Option, features apppkg.RuntimeFeatures, runtimeConfig *agentext.Config, logger telemetry.Logger) (cyberProfileConfig, error) {
	ioaConfig, err := ioaext.ConfigFromOption(option)
	if err != nil {
		return cyberProfileConfig{}, err
	}
	application := apppkg.AppConfig(option, features, logger)
	return cyberProfileConfig{
		Option: option, Application: application,
		IOA: ioaConfig, Runtime: cloneConfig(runtimeConfig), Logger: logger,
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
	runtime    *agentext.Runtime
	proxy      *proxytool.ProxyHub
	ioa        *ioatools.Runtime
	tui        *tuiext.Extension
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
	config, err := profileConfigFromOption(request.Option, request.Features, request.Runtime, request.Logger)
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
	if config.Logger == nil {
		config.Logger = telemetry.NopLogger()
	}
	if config.Application.Capabilities.Empty() {
		config.Application.Capabilities = edition.Catalog()

	}
	if config.IOA != nil && !config.Application.Capabilities.Enabled("ioa") {
		config.Application.Capabilities = capability.Must(append(config.Application.Capabilities.All(), ioaext.Descriptor())...)
	}
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
	signals := signalsext.New()
	hookRegistry := signals.Hooks()
	capture := config.Application.Tools.MitmCapture == nil || *config.Application.Tools.MitmCapture
	proxyExtension, err := proxyext.New(workDir, config.Application.Scanner.Proxy, capture, hookRegistry, config.Application.Tools.TrafficStorage)
	if err != nil {
		return nil, fmt.Errorf("construct proxy infrastructure: %w", err)
	}
	proxyHub := proxyExtension.Hub()
	events := signals.Events()
	var runtimeLoop agent.Loop
	if config.Runtime != nil {
		runtimeLoop = config.Runtime.Loop
	}
	scannerLoop := runtimeLoop
	if scannerLoop == nil && config.Application.Scanner.AIEnabled {
		scannerLoop = agent.StandardLoop{}
	}
	if config.IOA != nil {
		bundle, diagnostics := ioaext.Skills()
		if len(diagnostics) > 0 {
			return nil, fmt.Errorf("load IOA skills: %v", diagnostics)
		}
		config.Application.SkillBundles = append(append([]skills.Bundle(nil), config.Application.SkillBundles...), bundle)
	}
	applicationGraph, err := newApplicationGraph(config.Application, hookRegistry, events, proxyHub, scannerLoop, workDir)
	if err != nil {
		return nil, fmt.Errorf("construct Cyber application: %w", err)
	}
	application := applicationGraph.application

	var entries []extension.Entry
	entries = append(entries, extension.Entry{ID: "cyber.signals", Extension: signals})
	var sourceDependencies []string
	if strings.TrimSpace(config.Output) != "" {
		output, outputErr := telemetryext.New(events, telemetryext.Options{Path: config.Output})
		if outputErr != nil {
			return nil, outputErr
		}
		entries = append(entries, extension.Entry{ID: telemetryID, Extension: output})
		sourceDependencies = append(sourceDependencies, telemetryID)
	}
	if config.Artifacts != nil {
		projection, projectionErr := newArtifactProjection(events, config.Artifacts, config.Logger)
		if projectionErr != nil {
			return nil, projectionErr
		}
		entries = append(entries, extension.Entry{ID: artifactsID, DependsOn: append([]string(nil), sourceDependencies...), Extension: projection})
		sourceDependencies = append(sourceDependencies, artifactsID)
	}
	var observer *observeext.Extension
	if len(config.Observe) > 0 {
		var observeErr error
		observer, observeErr = observeext.New(hookRegistry, events, observeext.Options{Kinds: config.Observe, Logger: config.Logger})
		if observeErr != nil {
			return nil, observeErr
		}
		entries = append(entries, extension.Entry{ID: observeID, DependsOn: append([]string(nil), sourceDependencies...), Extension: observer})
		sourceDependencies = append(sourceDependencies, observeID)
	}
	entries = append(entries, extension.Entry{ID: proxyID, DependsOn: append([]string(nil), sourceDependencies...), Extension: proxyExtension})
	applicationDependencies := append([]string{proxyID}, sourceDependencies...)
	var ioa *ioaext.Extension
	if config.IOA != nil {
		deps := ioaext.Services{Commands: application.Commands, Events: events, Logger: config.Logger}
		if config.Runtime != nil {
			deps.Deliver = func(ctx context.Context, message inbox.Message) error {
				if product.extensions == nil || !product.extensions.Active() {
					return agentext.ErrUnavailable
				}
				return product.runtime.Deliver(ctx, message)
			}
		}
		ioa, err = ioaext.New(*config.IOA, deps)
		if err != nil {
			return nil, fmt.Errorf("construct IOA extension: %w", err)
		}
		entries = append(entries, extension.Entry{ID: ioaID, Extension: ioa})
		applicationDependencies = append(applicationDependencies, ioaID)
	}
	// The profile contributes all capability entries directly to its sole Set.
	// Registries activate after every declaration and drain before resources.
	applicationEntries, applicationReadyID := applicationGraph.entriesFor(applicationID, applicationDependencies...)
	entries = append(entries, applicationEntries...)
	var run *agentext.Runtime
	var ioaRuntime *ioatools.Runtime
	const tuiID = "cyber.tui"
	if config.Runtime != nil || ioa != nil {
		product.tui = tuiext.New()
		entries = append(entries, extension.Entry{ID: tuiID, Extension: product.tui})
	}
	if ioa != nil {
		ioaRuntime = ioa.Runtime()
		presentation, err := ioaconsole.New(product.tui.Registrar(), ioaRuntime, config.IOA.Space, config.IOA.URL)
		if err != nil {
			return nil, err
		}
		entries = append(entries, extension.Entry{ID: "cyber.ioa-repl", DependsOn: []string{tuiID, ioaID}, Extension: presentation})
	}
	if config.Runtime != nil {
		agentConfig := *config.Runtime
		agentConfig.Application = application
		agentConfig.NodeName = nodeName
		if config.IOA != nil && config.IOA.Space != "" {
			agentConfig.Preamble = strings.TrimSpace(agentConfig.Preamble + "\n" + ioaext.Preamble(*config.IOA))
		}

		agentConfig.Option, agentConfig.Logger = config.Option, config.Logger
		if runtimeLoop != nil {
			loopExtension, err := loopext.New(loopext.Config{Loop: runtimeLoop})
			if err != nil {
				return nil, err
			}
			agentConfig.Loop = loopExtension.Runtime()
			entries = append(entries, extension.Entry{ID: agentID + ".loop", DependsOn: []string{applicationReadyID}, Extension: loopExtension})
		} else {
			agentConfig.Loop = nil
		}
		agentExtension, err := agentext.New(agentConfig)
		if err != nil {
			return nil, fmt.Errorf("construct Cyber runtime: %w", err)
		}
		run = agentExtension.Runtime()
		sessionDependencies := []string{applicationReadyID}
		if runtimeLoop != nil {
			sessionDependencies = append(sessionDependencies, agentID+".loop")
		}
		entries = append(entries, extension.Entry{
			ID: agentID, DependsOn: sessionDependencies,
			Extension: agentExtension,
		})
		presentation, err := sessionconsole.New(product.tui.Registrar(), run)
		if err != nil {
			return nil, err
		}
		entries = append(entries, extension.Entry{ID: "cyber.session-repl", DependsOn: []string{tuiID, agentID}, Extension: presentation})
	}
	extensions, err := extension.New(entries...)
	if err != nil {
		return nil, err
	}
	product.extensions, product.app, product.runtime, product.proxy, product.ioa = extensions, application, run, proxyHub, ioaRuntime
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

func (p *cyberProfile) Runtime() (*agentext.Runtime, error) {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return nil, fmt.Errorf("Cyber profile is not active")
	}
	if p.runtime == nil {
		return nil, fmt.Errorf("Cyber profile has no Agent runtime")
	}
	return p.runtime, nil
}

func (p *cyberProfile) RegisterResourceNamespaces(mux *aop.NamespaceMux) error {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return fmt.Errorf("Cyber profile is not active")
	}
	return proxytool.RegisterTrafficNamespace(mux, p.proxy)
}

func (p *cyberProfile) Close(ctx context.Context) error {
	if p == nil || p.extensions == nil {
		return nil
	}
	return p.extensions.Close(ctx)
}

func cloneConfig(config *agentext.Config) *agentext.Config {
	if config == nil {
		return nil
	}
	cloned := *config
	cloned.BaseSkills = append([]string(nil), config.BaseSkills...)
	if config.PromptConfig != nil {
		prompt := *config.PromptConfig
		prompt.LoadedSkills = append([]agentext.LoadedSkill(nil), config.PromptConfig.LoadedSkills...)
		cloned.PromptConfig = &prompt
	}
	return &cloned
}

func (p *cyberProfile) AgentStatus() *aop.AgentStatus {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return &aop.AgentStatus{}
	}
	status := agentext.AgentStatus(p.app)
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

func (p *cyberProfile) Capabilities() []string {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return nil
	}
	var capabilities []string
	if p.ioa != nil {
		capabilities = append(capabilities, "ioa")
	}
	if p.proxy != nil {
		capabilities = append(capabilities, "traffic")
	}
	return capabilities
}
