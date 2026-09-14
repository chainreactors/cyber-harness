package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	coreevents "github.com/chainreactors/aiscan/core/events"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/edition"
	agentext "github.com/chainreactors/aiscan/pkg/exts/agent"
	eventoutput "github.com/chainreactors/aiscan/pkg/exts/eventoutput"
	ioaext "github.com/chainreactors/aiscan/pkg/exts/ioa"
	observeext "github.com/chainreactors/aiscan/pkg/exts/observe"
	proxyext "github.com/chainreactors/aiscan/pkg/exts/proxy"
	profilepkg "github.com/chainreactors/aiscan/pkg/profile"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
	ioatools "github.com/chainreactors/aiscan/tools/ioa"
	proxytool "github.com/chainreactors/aiscan/tools/proxy"
)

const (
	eventOutputID = "aiscan.event-output"
	artifactsID   = "aiscan.artifacts"
	observeID     = "aiscan.observe"
	proxyID       = "aiscan.proxy"
	applicationID = "aiscan.application"
	ioaID         = "aiscan.ioa"
	agentID       = "aiscan.agent"
)

type aiscanProfileConfig struct {
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

func profileConfigFromOption(option *cfg.Option, features apppkg.RuntimeFeatures, runtimeConfig *agentext.Config, logger telemetry.Logger) aiscanProfileConfig {
	application := apppkg.AppConfig(option, features, logger)
	return aiscanProfileConfig{
		Option: option, Application: application,
		IOA: ioatools.ConfigFromOption(option), Runtime: cloneConfig(runtimeConfig), Logger: logger,
		Observe: parseObserve(option.Observe), Output: resolveOutputPath(option),
	}
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

type aiscanProfile struct {
	assembly *profilepkg.Assembly
	app      *apppkg.App
	runtime  *agentext.Runtime
	proxy    *proxytool.ProxyHub
}

var _ profilepkg.Application = (*aiscanProfile)(nil)

var aiscanProfileFactory profilepkg.Factory = func(request profilepkg.Request) (profilepkg.Application, error) {
	return newAIScanProfile(profileConfigFromOption(request.Option, request.Features, request.Runtime, request.Logger))
}

func newAIScanProfile(config aiscanProfileConfig) (*aiscanProfile, error) {
	if config.Option == nil {
		return nil, fmt.Errorf("aiscan profile option is required")
	}
	if config.Logger == nil {
		config.Logger = telemetry.NopLogger()
	}
	if config.Application.Capabilities.Empty() {
		config.Application.Capabilities = edition.Catalog()
	}
	config.Runtime = cloneConfig(config.Runtime)
	workDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve AIScan working directory: %w", err)
	}
	hookRegistry := hooks.New()
	capture := config.Application.Tools.MitmCapture == nil || *config.Application.Tools.MitmCapture
	proxyExtension, err := proxyext.New(workDir, config.Application.Scanner.Proxy, capture, hookRegistry, config.Application.Tools.TrafficStorage)
	if err != nil {
		return nil, fmt.Errorf("construct proxy infrastructure: %w", err)
	}
	proxyHub := proxyExtension.Hub()
	events := coreevents.New()
	var runtimeLoop agent.Loop
	if config.Runtime != nil {
		runtimeLoop = config.Runtime.Loop
	}
	scannerLoop := runtimeLoop
	if scannerLoop == nil && config.Application.Scanner.AIEnabled {
		scannerLoop = agent.StandardLoop{}
	}
	applicationGraph, err := newApplicationGraph(config.Application, hookRegistry, events, proxyHub, scannerLoop, workDir)
	if err != nil {
		return nil, fmt.Errorf("construct AIScan application: %w", err)
	}
	application := applicationGraph.application

	var entries []extension.Entry
	var sourceDependencies []string
	if strings.TrimSpace(config.Output) != "" {
		output, outputErr := eventoutput.New(events, eventoutput.Options{Path: config.Output})
		if outputErr != nil {
			return nil, outputErr
		}
		entries = append(entries, extension.Entry{ID: eventOutputID, Extension: output})
		sourceDependencies = append(sourceDependencies, eventOutputID)
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
		ioa, err = ioaext.New(*config.IOA, application.Commands, config.Logger)
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
	if ioa != nil {
		ioaRuntime = ioa.Runtime()
	}
	if config.Runtime != nil {
		agentConfig := *config.Runtime
		agentConfig.Application, agentConfig.IOA = application, ioaRuntime
		agentConfig.Option, agentConfig.Logger = config.Option, config.Logger
		agentConfig.Loop = runtimeLoop
		agentExtension, err := agentext.New(agentConfig)
		if err != nil {
			return nil, fmt.Errorf("construct AIScan runtime: %w", err)
		}
		run = agentExtension.Runtime()
		entries = append(entries, extension.Entry{
			ID: agentID, DependsOn: []string{applicationReadyID},
			Extension: agentExtension,
		})
	}
	assembly, err := profilepkg.Assemble(entries...)
	if err != nil {
		return nil, err
	}
	return &aiscanProfile{assembly: assembly, app: application, runtime: run, proxy: proxyHub}, nil
}

func (p *aiscanProfile) Load(ctx context.Context) error {
	if p == nil || p.assembly == nil {
		return fmt.Errorf("AIScan profile is required")
	}
	return p.assembly.Load(ctx)
}

func (p *aiscanProfile) App() (*apppkg.App, error) {
	if p == nil || p.assembly == nil || !p.assembly.Available() || p.app == nil {
		return nil, fmt.Errorf("AIScan profile is not active")
	}
	return p.app, nil
}

func (p *aiscanProfile) Runtime() (*agentext.Runtime, error) {
	if p == nil || p.assembly == nil || !p.assembly.Available() {
		return nil, fmt.Errorf("AIScan profile is not active")
	}
	if p.runtime == nil {
		return nil, fmt.Errorf("AIScan profile has no Agent runtime")
	}
	return p.runtime, nil
}

func (p *aiscanProfile) RegisterResourceNamespaces(mux *aop.NamespaceMux) error {
	if p == nil || p.assembly == nil || !p.assembly.Available() {
		return fmt.Errorf("AIScan profile is not active")
	}
	return proxytool.RegisterTrafficNamespace(mux, p.proxy)
}

func (p *aiscanProfile) Close(ctx context.Context) error {
	if p == nil || p.assembly == nil {
		return nil
	}
	return p.assembly.Close(ctx)
}

func cloneConfig(config *agentext.Config) *agentext.Config {
	if config == nil {
		return nil
	}
	cloned := *config
	if config.PromptConfig != nil {
		prompt := *config.PromptConfig
		prompt.LoadedSkills = append([]agentext.LoadedSkill(nil), config.PromptConfig.LoadedSkills...)
		cloned.PromptConfig = &prompt
	}
	return &cloned
}
