// Package aiscan is the AIScan composition root. It constructs a fixed extension
// graph and owns every edition-selected capability extension.
package aiscan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	ioatools "github.com/chainreactors/aiscan/tools/ioa"
)

const (
	EventOutputID = "aiscan.event-output"
	ObserveID     = "aiscan.observe"
	ProxyID       = "aiscan.proxy"
	ApplicationID = "aiscan.application"
	IOAID         = "aiscan.ioa"
	AgentID       = "aiscan.agent"
	RuntimeID     = "aiscan.runtime"
)

type Config struct {
	Option      *cfg.Option
	Application apppkg.Config
	IOA         *ioatools.Config
	// Runtime nil creates the application-only profile used by the Web service.
	Runtime *sessionext.Config
	Logger  telemetry.Logger
	Observe []observeext.Kind
	Output  string
}

func FromOption(option *cfg.Option, features apppkg.RuntimeFeatures, runtimeConfig *sessionext.Config, logger telemetry.Logger) Config {
	application := apppkg.AppConfig(option, features, logger)
	return Config{
		Option: option, Application: application,
		IOA: ioatools.ConfigFromOption(option), Runtime: cloneConfig(runtimeConfig), Logger: logger,
		Observe: parseObserve(option.Observe), Output: resolveOutputPath(option),
	}
}

func resolveOutputPath(option *cfg.Option) string {
	if option == nil {
		return ""
	}
	if path := strings.TrimSpace(option.OutputFile); path != "" {
		return path
	}
	if option.Ephemeral || !option.SaveSession {
		return ""
	}
	name := "session-" + time.Now().Format("20060102-150405.000000000") + ".jsonl"
	return filepath.Join(cfg.DataDir(), "sessions", name)
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

// Profile owns one fixed graph through Set. App and runtime are active business
// entry points; Set alone owns their Load/Close ordering.
type Profile struct {
	set    *extension.Set
	access sync.RWMutex
	// Publication state prevents exposing a partially loaded or closing graph.
	// It does not track individual instance lifetimes; Set owns those states.
	active  bool
	closing bool
	app     *apppkg.App
	runtime *sessionext.Manager
	proxy   *proxyext.Extension
}

func New(config Config) (*Profile, error) {
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
	var selectedLoop agent.Loop
	if config.Runtime != nil {
		selectedLoop = config.Runtime.Loop
	}
	if selectedLoop == nil && config.Application.Scanner.AIEnabled {
		selectedLoop = agent.StandardLoop{}
	}
	var agentExtension *agentext.Extension
	var managedLoop agent.Loop
	if selectedLoop != nil {
		agentExtension = agentext.New(selectedLoop)
		managedLoop = agentExtension.Loop()
	}
	assembly, err := newApplicationAssembly(config.Application, hookRegistry, events, proxyHub, managedLoop, workDir)
	if err != nil {
		return nil, fmt.Errorf("construct AIScan application: %w", err)
	}
	application := assembly.application

	var entries []extension.Entry
	var sourceDependencies []string
	if strings.TrimSpace(config.Output) != "" {
		output, outputErr := eventoutput.New(events, eventoutput.Options{Path: config.Output})
		if outputErr != nil {
			return nil, outputErr
		}
		entries = append(entries, extension.Entry{ID: EventOutputID, Extension: output})
		sourceDependencies = append(sourceDependencies, EventOutputID)
	}
	var observer *observeext.Extension
	if len(config.Observe) > 0 {
		var observeErr error
		observer, observeErr = observeext.New(hookRegistry, events, observeext.Options{Kinds: config.Observe})
		if observeErr != nil {
			return nil, observeErr
		}
		entries = append(entries, extension.Entry{ID: ObserveID, DependsOn: append([]string(nil), sourceDependencies...), Extension: observer})
		sourceDependencies = append(sourceDependencies, ObserveID)
	}
	entries = append(entries, extension.Entry{ID: ProxyID, DependsOn: append([]string(nil), sourceDependencies...), Extension: proxyExtension})
	applicationDependencies := append([]string{ProxyID}, sourceDependencies...)
	var ioa *ioaext.Extension
	if config.IOA != nil {
		ioa, err = ioaext.New(*config.IOA, application.Commands, config.Logger)
		if err != nil {
			return nil, fmt.Errorf("construct IOA extension: %w", err)
		}
		entries = append(entries, extension.Entry{ID: IOAID, Extension: ioa})
		applicationDependencies = append(applicationDependencies, IOAID)
	}
	// The profile contributes all capability entries directly to its sole Set.
	// Registries activate after every declaration and drain before resources.
	applicationEntries, applicationReadyID := assembly.graph(ApplicationID, applicationDependencies...)
	entries = append(entries, applicationEntries...)
	if agentExtension != nil {
		entries = append(entries, extension.Entry{
			ID: AgentID, DependsOn: []string{applicationReadyID}, Extension: agentExtension,
		})
	}
	var run *sessionext.Manager
	var ioaRuntime *ioatools.Runtime
	if ioa != nil {
		ioaRuntime = ioa.Runtime()
	}
	if config.Runtime != nil {
		dependencies := []string{applicationReadyID}
		if config.Runtime.Loop != nil {
			config.Runtime.Loop = managedLoop
			dependencies = append(dependencies, AgentID)
		}
		runResource, runErr := sessionext.New(application, ioaRuntime, config.Option, config.Logger, *config.Runtime)
		err = runErr
		if err != nil {
			return nil, fmt.Errorf("construct AIScan runtime: %w", err)
		}
		run = runResource.Manager
		entries = append(entries, extension.Entry{
			ID: RuntimeID, DependsOn: dependencies,
			Extension: runResource,
		})
	}
	set, err := extension.New(entries...)
	if err != nil {
		return nil, err
	}
	return &Profile{set: set, app: application, runtime: run, proxy: proxyExtension}, nil
}

func cloneConfig(config *sessionext.Config) *sessionext.Config {
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

func (p *Profile) Load(ctx context.Context) error {
	if p == nil {
		return fmt.Errorf("aiscan profile is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := p.set.Load(ctx); err != nil {
		return err
	}
	p.access.Lock()
	if !p.closing {
		p.active = true
	}
	p.access.Unlock()
	return nil
}

func (p *Profile) App() (*apppkg.App, error) {
	if p == nil {
		return nil, fmt.Errorf("aiscan profile is required")
	}
	p.access.RLock()
	defer p.access.RUnlock()
	if !p.active || p.closing || p.app == nil {
		return nil, fmt.Errorf("aiscan profile is not active")
	}
	return p.app, nil
}

func (p *Profile) Runtime() (*sessionext.Manager, error) {
	if p == nil {
		return nil, fmt.Errorf("aiscan profile is required")
	}
	p.access.RLock()
	defer p.access.RUnlock()
	if !p.active || p.closing {
		return nil, fmt.Errorf("aiscan profile is not active")
	}
	if p.runtime == nil {
		return nil, fmt.Errorf("aiscan profile has no Agent runtime")
	}
	return p.runtime, nil
}

// RegisterResourceNamespaces installs protocols backed by resources owned by
// this active profile. Session namespaces remain owned by the runtime.
func (p *Profile) RegisterResourceNamespaces(mux *aop.NamespaceMux) error {
	if p == nil {
		return fmt.Errorf("aiscan profile is required")
	}
	p.access.RLock()
	if !p.active || p.closing || p.proxy == nil {
		p.access.RUnlock()
		return fmt.Errorf("aiscan profile is not active")
	}
	proxy := p.proxy
	p.access.RUnlock()
	return proxy.RegisterNamespaces(mux)
}

func (p *Profile) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.access.Lock()
	p.closing = true
	p.active = false
	p.access.Unlock()
	return p.set.Close(ctx)
}
