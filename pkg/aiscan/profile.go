package aiscan

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/aop"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/namespaces"
	"github.com/chainreactors/cyber/core/telemetry"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	ioaext "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	ioaconsole "github.com/chainreactors/cyber/pkg/exts/ioa/client/console"
	observeext "github.com/chainreactors/cyber/pkg/exts/observe"
	proxyext "github.com/chainreactors/cyber/pkg/exts/proxy"
	ptyext "github.com/chainreactors/cyber/pkg/exts/pty"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	sessionconsole "github.com/chainreactors/cyber/pkg/exts/session/console"
	telemetryext "github.com/chainreactors/cyber/pkg/exts/telemetry"
	tuiext "github.com/chainreactors/cyber/pkg/exts/tui"
	nodepkg "github.com/chainreactors/cyber/pkg/node"
	profilepkg "github.com/chainreactors/cyber/pkg/profile"
	ioatools "github.com/chainreactors/cyber/tools/ioa"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
)

type config struct {
	Option *cfg.Option
	Base   appConfig
	IOA    *ioatools.Config
	// Session nil creates the application-only profile used by the Web service.
	Session *agentsession.Config
	Observe []observeext.Kind
	Output  string
}

// Request is the complete host input for the reference distribution. Option
// must already be resolved by the host's declaration graph.
type Request struct {
	Option       *cfg.Option
	ProviderMode provider.StartupMode
	Session      *agentsession.Config
	Logger       telemetry.Logger
	SkipEngines  bool
	DisableIOA   bool
}

func configFromOption(option *cfg.Option, providerMode provider.StartupMode, sessionConfig *agentsession.Config, logger telemetry.Logger) (config, error) {
	if option == nil {
		return config{}, fmt.Errorf("cyber profile option is required")
	}
	ioaConfig, err := ioaext.ConfigFromOption(option)
	if err != nil {
		return config{}, err
	}
	application := appConfigFromOption(option, providerMode, logger)
	return config{
		Option: option, Base: application,
		IOA: ioaConfig, Session: sessionConfig,
		Observe: parseObserve(option.Observe), Output: strings.TrimSpace(option.OutputFile),
	}, nil
}

func parseObserve(value string) []observeext.Kind {
	var result []observeext.Kind
	for item := range strings.SplitSeq(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, observeext.Kind(item))
		}
	}
	return result
}

// Profile owns one reference-distribution extension graph.
type Profile struct {
	extensions *extension.Set
	app        *apppkg.State
	commands   commands.Executor
	bash       *terminaltool.BashTool
	runtime    *agentsession.Runtime
	ioa        *ioatools.Service
	bindings   *consoleapi.Registry
	namespaces *namespaces.Registry
}

var _ profilepkg.Profile = (*Profile)(nil)

// New constructs an unpublished reference-distribution profile. The caller
// owns the result and must close it, including after a failed Load.
func New(request Request) (*Profile, error) {
	switch request.ProviderMode {
	case provider.StartupDisabled, provider.StartupRequired, provider.StartupOptional:
	default:
		return nil, fmt.Errorf("invalid provider mode %d", request.ProviderMode)
	}
	if request.Option == nil {
		return nil, fmt.Errorf("cyber profile option is required")
	}
	if request.Option.Resolved == nil {
		return nil, fmt.Errorf("cyber profile option must be resolved by the host")
	}
	config, err := configFromOption(request.Option, request.ProviderMode, request.Session, request.Logger)
	if err != nil {
		return nil, err
	}
	config.Base.SkipEngines = request.SkipEngines
	if request.DisableIOA {
		config.IOA = nil
	}
	return newProfile(config)
}

func newProfile(config config) (*Profile, error) {
	if config.Option == nil {
		return nil, fmt.Errorf("cyber profile option is required")
	}
	config.Session = cloneConfig(config.Session)
	if config.Session != nil {
		config.Session.BaseSkills = append([]string{"cyber"}, config.Session.BaseSkills...)
	}
	if config.Base.Logger == nil {
		config.Base.Logger = telemetry.NopLogger()
	}
	logger := config.Base.Logger
	p := &Profile{}
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
	capture := config.Base.Tools.MitmCapture == nil || *config.Base.Tools.MitmCapture
	proxyExtension := proxyext.New(proxyext.Config{
		WorkDir: workDir, Proxy: config.Base.Scanner.Resources.Proxy,
		Capture: capture, Storage: config.Base.Tools.TrafficStorage,
	})
	// One loop for the profile: the scanner and the session run against the
	// same installation rather than each being handed its own. A profile that
	// selects sessions without reasoning selects NoLoop, which declines every
	// run -- absence is a value here, not a nil.
	loop := agent.Loop(agent.NoLoop())
	switch {
	case config.Session != nil && config.Session.Loop != nil:
		loop = config.Session.Loop
	case config.Session == nil && config.Base.Provider.Mode != provider.StartupDisabled:
		loop = agent.StandardLoop{}
	}
	graph, err := extensions(config.Base, loop, workDir, proxyExtension)
	if err != nil {
		return nil, fmt.Errorf("construct Cyber application: %w", err)
	}

	// The graph publishes the capabilities everything else borrows, so it comes
	// first. The observers follow it: they subscribe to hooks and to the event
	// stream, both of which fire at run time rather than during load.
	namespaceRegistry := namespaces.New()
	values := append([]extension.Extension{namespaceRegistry}, graph...)
	if strings.TrimSpace(config.Output) != "" {
		output, err := telemetryext.New(telemetryext.Options{Path: config.Output})
		if err != nil {
			return nil, err
		}
		values = append(values, output)
	}
	if len(config.Observe) > 0 {
		observer, err := observeext.New(observeext.Options{Kinds: config.Observe, Logger: logger})
		if err != nil {
			return nil, err
		}
		values = append(values, observer)
	}
	values = append(values, ptyext.New())
	var ioa *ioaext.Extension
	if config.IOA != nil {
		bundle, diagnostics := ioaext.Skills()
		if len(diagnostics) > 0 {
			return nil, fmt.Errorf("load IOA skills: %v", diagnostics)
		}
		deps := ioaext.Dependencies{Logger: logger, Skills: []skills.Bundle{bundle}}
		if config.Session != nil {
			deps.Deliver = func(ctx context.Context, message inbox.Message) error {
				if p.extensions == nil || !p.extensions.Active() {
					return agentsession.ErrUnavailable
				}
				return p.runtime.Deliver(ctx, message)
			}
		}
		ioa = ioaext.New(*config.IOA, deps)
		values = append(values, ioa)
	}
	presents := config.Session != nil || ioa != nil
	if presents {
		values = append(values, tuiext.New())
	}
	if ioa != nil {
		p.ioa = ioa.Service()
		presentation, err := ioaconsole.New(p.ioa, config.IOA.Space, config.IOA.URL)
		if err != nil {
			return nil, err
		}
		values = append(values, presentation)
	}
	if config.Session != nil {
		agentConfig := *config.Session
		agentConfig.NodeName = nodeName
		agentConfig.Option, agentConfig.Logger = config.Option, logger
		values = append(values, sessionext.New(agentConfig))
		values = append(values, extension.Func{LoadFunc: func(scope *extension.Scope) error {
			runtime, err := extension.Use[*agentsession.Runtime](scope)
			if err != nil {
				return err
			}
			return extension.Add(scope, runtime.NamespaceBindings()...)
		}})
		values = append(values, sessionconsole.New())
	}
	// Last in the slice, so it borrows after everything is published and
	// releases before anything is torn down. This is how a composition root
	// reads what the graph assembled without reaching into the registry.
	values = append(values, extension.Func{LoadFunc: func(scope *extension.Scope) error {
		application, err := extension.Use[*apppkg.State](scope)
		if err != nil {
			return err
		}
		p.app = application
		if p.commands, err = extension.Use[commands.Executor](scope); err != nil {
			return err
		}
		// Borrowed under the same condition that installed the owner: this is
		// the root reading back its own membership decision, not a consumer
		// tolerating a capability that may be missing.
		if presents {
			if p.bindings, err = extension.Use[*consoleapi.Registry](scope); err != nil {
				return err
			}
		}
		if p.bash, err = extension.Use[*terminaltool.BashTool](scope); err != nil {
			return err
		}
		if config.Session == nil {
			return nil
		}
		p.runtime, err = extension.Use[*agentsession.Runtime](scope)
		return err
	}})
	set, err := extension.New(values...)
	if err != nil {
		return nil, err
	}
	p.extensions, p.namespaces = set, namespaceRegistry
	return p, nil
}

func (p *Profile) Load(ctx context.Context) error {
	if p == nil || p.extensions == nil {
		return fmt.Errorf("Cyber profile is required")
	}
	return p.extensions.Load(ctx)
}

func (p *Profile) State() (*apppkg.State, error) {
	if p == nil || p.extensions == nil || !p.extensions.Active() || p.app == nil {
		return nil, fmt.Errorf("Cyber profile is not active")
	}
	return p.app, nil
}

func (p *Profile) Runtime() (*agentsession.Runtime, error) {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return nil, fmt.Errorf("Cyber profile is not active")
	}
	if p.runtime == nil {
		return nil, fmt.Errorf("Cyber profile has no Agent runtime")
	}
	return p.runtime, nil
}

func (p *Profile) RegisterNamespaces(mux *aop.NamespaceMux) error {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return fmt.Errorf("Cyber profile is not active")
	}
	return p.namespaces.Bind(mux)
}

func (p *Profile) Close(ctx context.Context) error {
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
	return &cloned
}

func (p *Profile) AgentStatus() *aop.AgentStatus {
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
func (p *Profile) ConsoleBindings() *consoleapi.Bindings {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return nil
	}
	if p.bindings == nil {
		return nil
	}
	return p.bindings.Bindings()
}

// Shell is the command surface a host runs scanner subcommands through. It is
// how a profile without a session runtime still executes commands: the registry
// and the tool are capabilities, published whether or not an agent is running.
func (p *Profile) Shell() (commands.Executor, *terminaltool.BashTool) {
	if p == nil {
		return nil, nil
	}
	return p.commands, p.bash
}
