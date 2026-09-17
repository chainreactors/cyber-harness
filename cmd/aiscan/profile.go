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
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/aop"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/namespaces"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
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

type cyberProfileConfig struct {
	Option      *cfg.Option
	Application appConfig
	IOA         *ioatools.Config
	// Session nil creates the application-only profile used by the Web service.
	Session   *agentsession.Config
	Observe   []observeext.Kind
	Output    string
	Artifacts coretool.ArtifactImporter
}

func profileConfigFromOption(option *cfg.Option, providerMode profilepkg.ProviderMode, sessionConfig *agentsession.Config, logger telemetry.Logger) (cyberProfileConfig, error) {
	ioaConfig, err := ioaext.ConfigFromOption(option)
	if err != nil {
		return cyberProfileConfig{}, err
	}
	application := appConfigFromOption(option, providerMode, logger)
	return cyberProfileConfig{
		Option: option, Application: application,
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

type cyberProfile struct {
	extensions *extension.Set
	app        *apppkg.App
	commands   commands.Executor
	bash       *terminaltool.BashTool
	runtime    *agentsession.Runtime
	ioa        *ioatools.Service
	bindings   *consoleapi.Registry
	namespaces *namespaces.Registry
}

var _ profilepkg.Application = (*cyberProfile)(nil)

var cyberProfileFactory profilepkg.Factory = func(request profilepkg.Request) (profilepkg.Application, error) {
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
	config, err := profileConfigFromOption(request.Option, request.ProviderMode, request.Session, request.Logger)
	if err != nil {
		return nil, err
	}
	return newCyberProfile(config)
}

func newCyberProfile(config cyberProfileConfig) (*cyberProfile, error) {
	if config.Option == nil {
		return nil, fmt.Errorf("cyber profile option is required")
	}
	config.Session = cloneConfig(config.Session)
	if config.Session != nil {
		config.Session.BaseSkills = append([]string{"cyber"}, config.Session.BaseSkills...)
	}
	if config.Application.Logger == nil {
		config.Application.Logger = telemetry.NopLogger()
	}
	logger := config.Application.Logger
	p := &cyberProfile{}
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
	capture := config.Application.Tools.MitmCapture == nil || *config.Application.Tools.MitmCapture
	proxyExtension := proxyext.New(proxyext.Config{
		WorkDir: workDir, Proxy: config.Application.Scanner.Resources.Proxy,
		Capture: capture, Storage: config.Application.Tools.TrafficStorage,
	})
	// One loop for the profile: the scanner and the session run against the
	// same installation rather than each being handed its own. A profile that
	// selects sessions without reasoning selects NoLoop, which declines every
	// run -- absence is a value here, not a nil.
	loop := agent.Loop(agent.NoLoop())
	switch {
	case config.Session != nil && config.Session.Loop != nil:
		loop = config.Session.Loop
	case config.Session == nil && config.Application.Provider.Mode != provider.StartupDisabled:
		loop = agent.StandardLoop{}
	}
	graph, err := newAppGraph(config.Application, loop, workDir, proxyExtension)
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
	if config.Artifacts != nil {
		projection, err := newArtifactProjection(config.Artifacts, logger)
		if err != nil {
			return nil, err
		}
		values = append(values, projection)
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
		if config.IOA != nil && config.IOA.Space != "" {
			agentConfig.Preamble = strings.TrimSpace(agentConfig.Preamble + "\n" + ioaext.Preamble(*config.IOA))
		}

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
		application, err := extension.Use[*apppkg.App](scope)
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
	if p.bindings == nil {
		return nil
	}
	return p.bindings.Bindings()
}

// Shell is the command surface a host runs scanner subcommands through. It is
// how a profile without a session runtime still executes commands: the registry
// and the tool are capabilities, published whether or not an agent is running.
func (p *cyberProfile) Shell() (commands.Executor, *terminaltool.BashTool) {
	if p == nil {
		return nil, nil
	}
	return p.commands, p.bash
}
