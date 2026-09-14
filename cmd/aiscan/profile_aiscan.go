package main

import (
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
	sessionext "github.com/chainreactors/aiscan/pkg/exts/session"
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
	sessionID     = "aiscan.session"
)

type productProfileConfig struct {
	Option      *cfg.Option
	Application apppkg.Config
	IOA         *ioatools.Config
	// Session nil creates the application-only profile used by the Web service.
	Session   *sessionext.Config
	Logger    telemetry.Logger
	Observe   []observeext.Kind
	Output    string
	Artifacts managementapi.ArtifactImporter
}

func profileConfigFromOption(option *cfg.Option, features apppkg.RuntimeFeatures, sessionConfig *sessionext.Config, logger telemetry.Logger) productProfileConfig {
	application := apppkg.AppConfig(option, features, logger)
	return productProfileConfig{
		Option: option, Application: application,
		IOA: ioatools.ConfigFromOption(option), Session: cloneSessionConfig(sessionConfig), Logger: logger,
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

var productProfileFactory profilepkg.Factory = func(request profilepkg.Request) (*profilepkg.Profile, error) {
	return newProductProfile(profileConfigFromOption(request.Option, request.Features, request.Session, request.Logger))
}

func newProductProfile(config productProfileConfig) (*profilepkg.Profile, error) {
	if config.Option == nil {
		return nil, fmt.Errorf("aiscan profile option is required")
	}
	if config.Logger == nil {
		config.Logger = telemetry.NopLogger()
	}
	if config.Application.Capabilities.Empty() {
		config.Application.Capabilities = edition.Catalog()
	}
	config.Session = cloneSessionConfig(config.Session)
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
	var selectedLoop agent.Loop
	if config.Session != nil {
		selectedLoop = config.Session.Loop
	}
	if selectedLoop == nil && config.Application.Scanner.AIEnabled {
		selectedLoop = agent.StandardLoop{}
	}
	var agentExtension *agentext.Extension
	var admittedLoop agent.Loop
	if selectedLoop != nil {
		agentExtension, err = agentext.New(selectedLoop)
		if err != nil {
			return nil, fmt.Errorf("construct Agent extension: %w", err)
		}
		admittedLoop = agentExtension.Runtime()
	}
	applicationGraph, err := newApplicationGraph(config.Application, hookRegistry, events, proxyHub, admittedLoop, workDir)
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
	if agentExtension != nil {
		entries = append(entries, extension.Entry{ID: agentID, Extension: agentExtension})
		applicationDependencies = append(applicationDependencies, agentID)
	}
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
	var run *sessionext.Runtime
	var ioaRuntime *ioatools.Runtime
	if ioa != nil {
		ioaRuntime = ioa.Runtime()
	}
	if config.Session != nil {
		sessionConfig := *config.Session
		sessionConfig.Application, sessionConfig.IOA = application, ioaRuntime
		sessionConfig.Option, sessionConfig.Logger = config.Option, config.Logger
		sessionConfig.Loop = admittedLoop
		sessionExtension, err := sessionext.New(sessionConfig)
		if err != nil {
			return nil, fmt.Errorf("construct Session extension: %w", err)
		}
		run = sessionExtension.Runtime()
		entries = append(entries, extension.Entry{
			ID: sessionID, DependsOn: []string{applicationReadyID},
			Extension: sessionExtension,
		})
	}
	return profilepkg.New(profilepkg.Config{
		Entries:  entries,
		App:      application,
		Sessions: run,
		RegisterResourceNamespaces: func(mux *aop.NamespaceMux) error {
			return proxytool.RegisterTrafficNamespace(mux, proxyHub)
		},
	})
}

func cloneSessionConfig(config *sessionext.Config) *sessionext.Config {
	if config == nil {
		return nil
	}
	cloned := *config
	if config.PromptConfig != nil {
		prompt := *config.PromptConfig
		prompt.LoadedSkills = append([]sessionext.LoadedSkill(nil), config.PromptConfig.LoadedSkills...)
		cloned.PromptConfig = &prompt
	}
	return &cloned
}
