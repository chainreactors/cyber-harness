package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/eventbus"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/namespaces"
	"github.com/chainreactors/cyber/core/proc"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	cfg "github.com/chainreactors/cyber/pkg/config"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	ioaclient "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	nativeext "github.com/chainreactors/cyber/pkg/exts/native"
	observeext "github.com/chainreactors/cyber/pkg/exts/observe"
	proxyext "github.com/chainreactors/cyber/pkg/exts/proxy"
	ptyext "github.com/chainreactors/cyber/pkg/exts/pty"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	subagentext "github.com/chainreactors/cyber/pkg/exts/subagent"
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

func configFromOption(option *cfg.Option, providerMode provider.StartupMode, sessionConfig *agentsession.Config, logger telemetry.Logger) (config, error) {
	if option == nil {
		return config{}, fmt.Errorf("cyber profile option is required")
	}
	ioaConfig, err := ioaclient.ConfigFromOption(option)
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

// aiscanProfile owns one reference-distribution extension graph.
type aiscanProfile struct {
	extensions *extension.Set
	providers  *provider.State
	events     *events.Stream
	progress   *eventbus.Bus[*toolpb.Progress]
	processes  *proc.Manager
	commands   coretool.CommandExecutor
	bash       *terminaltool.BashTool
	runtime    *agentsession.Runtime
	ioa        *ioatools.Service
	bindings   *consoleapi.Registry
	namespaces *namespaces.Registry
}

var _ profilepkg.Profile = (*aiscanProfile)(nil)

func buildAIScanProfile(config config) (*aiscanProfile, error) {
	if config.Option == nil {
		return nil, fmt.Errorf("cyber profile option is required")
	}
	config.Session = cloneConfig(config.Session)
	if config.Session != nil {
		config.Session.BaseSkills = append([]string{"cyber"}, config.Session.BaseSkills...)
	}
	p := &aiscanProfile{}
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
		observer, err := observeext.New(observeext.Options{Kinds: config.Observe})
		if err != nil {
			return nil, err
		}
		values = append(values, observer)
	}
	var ioa *ioaclient.Extension
	if config.IOA != nil {
		bundle, diagnostics := ioaclient.Skills()
		if len(diagnostics) > 0 {
			return nil, fmt.Errorf("load IOA skills: %v", diagnostics)
		}
		ioa = ioaclient.New(*config.IOA)
		values = append(values, ioa, ioaclient.NewCollaboration(ioaclient.CollaborationOptions{Skills: []skills.Bundle{bundle}}))
	}
	values = append(values, nativeext.New())
	presents := config.Session != nil || ioa != nil
	if presents {
		values = append(values, tuiext.New())
	}
	if ioa != nil {
		values = append(values, ioaclient.NewConsole(ioaclient.ConsoleConfig{Space: config.IOA.Space, Endpoint: config.IOA.URL}))
	}
	if config.Session != nil {
		values = append(values, ptyext.New())
		agentConfig := *config.Session
		agentConfig.NodeName = nodeName
		agentConfig = sessionext.ConfigFromOption(config.Option, agentConfig)
		values = append(values, sessionext.New(agentConfig), subagentext.NewTools())
		values = append(values, sessionext.NewProtocol())
		values = append(values, sessionext.NewConsole())
	}
	// Last in the slice, so it borrows after everything is published and
	// releases before anything is torn down. This is how a composition root
	// reads what the graph assembled without reaching into the registry.
	values = append(values, extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		if ioa != nil {
			p.ioa = ioa.Service()
		}
		if p.providers, err = extension.Use[*provider.State](scope); err != nil {
			return err
		}
		if p.events, err = extension.Use[*events.Stream](scope); err != nil {
			return err
		}
		if p.progress, err = extension.Use[*eventbus.Bus[*toolpb.Progress]](scope); err != nil {
			return err
		}
		if p.processes, err = extension.Use[*proc.Manager](scope); err != nil {
			return err
		}

		if p.commands, err = extension.Use[coretool.CommandExecutor](scope); err != nil {
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

func (p *aiscanProfile) Load(ctx context.Context) error {
	if p == nil || p.extensions == nil {
		return fmt.Errorf("Cyber profile is required")
	}
	return p.extensions.Load(ctx)
}

func (p *aiscanProfile) Runtime() (*agentsession.Runtime, error) {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return nil, fmt.Errorf("Cyber profile is not active")
	}
	if p.runtime == nil {
		return nil, fmt.Errorf("Cyber profile has no Agent runtime")
	}
	return p.runtime, nil
}

func (p *aiscanProfile) RegisterNamespaces(mux *aop.NamespaceMux) error {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return fmt.Errorf("Cyber profile is not active")
	}
	return p.namespaces.Bind(mux)
}

func (p *aiscanProfile) Close(ctx context.Context) error {
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

func (p *aiscanProfile) AgentStatus() *aop.AgentStatus {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return &aop.AgentStatus{}
	}
	status := nodepkg.AgentStatus(p.providers)
	if p.ioa != nil {
		collaboration := p.ioa.Status()
		status.Bound, status.Space = collaboration.Bound, collaboration.Space
	}
	return status
}

// ConsoleBindings lends the selected presentation contributions.
func (p *aiscanProfile) ConsoleBindings() *consoleapi.Bindings {
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
func (p *aiscanProfile) Shell() (coretool.CommandExecutor, *terminaltool.BashTool) {
	if p == nil {
		return nil, nil
	}
	return p.commands, p.bash
}

// newAIScanProfile resolves host inputs and constructs the product extension set.
func newAIScanProfile(request profilepkg.Request) (profilepkg.Profile, error) {
	switch request.ProviderMode {
	case provider.StartupDisabled, provider.StartupRequired, provider.StartupOptional:
	default:
		return nil, fmt.Errorf("invalid provider mode %d", request.ProviderMode)
	}
	if request.Option == nil {
		return nil, fmt.Errorf("cyber profile option is required")
	}
	if request.Option.Resolved == nil {
		resolved, err := defaultSections().ResolveValues(request.Option.Extensions, nil, nil)
		if err != nil {
			return nil, err
		}
		option := *request.Option
		option.Resolved = resolved
		option.Extensions = resolved.Values()
		request.Option = &option
	}
	config, err := configFromOption(request.Option, request.ProviderMode, request.Session, request.Logger)
	if err != nil {
		return nil, err
	}
	return buildAIScanProfile(config)
}

// Providers borrows a capability from the active installation.
func (p *aiscanProfile) Providers() (*provider.State, error) {
	if !p.Active() {
		return nil, fmt.Errorf("profile is not active")
	}
	return p.providers, nil
}

// Events borrows a capability from the active installation.
func (p *aiscanProfile) Events() (*events.Stream, error) {
	if !p.Active() {
		return nil, fmt.Errorf("profile is not active")
	}
	return p.events, nil
}

// Progress borrows a capability from the active installation.
func (p *aiscanProfile) Progress() (*eventbus.Bus[*toolpb.Progress], error) {
	if !p.Active() {
		return nil, fmt.Errorf("profile is not active")
	}
	return p.progress, nil
}

// Processes borrows a capability from the active installation.
func (p *aiscanProfile) Processes() (*proc.Manager, error) {
	if !p.Active() {
		return nil, fmt.Errorf("profile is not active")
	}
	return p.processes, nil
}

func (p *aiscanProfile) Active() bool {
	return p != nil && p.extensions != nil && p.extensions.Active()
}
