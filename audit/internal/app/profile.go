package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	crtm "github.com/chainreactors/crtm/pkg"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/audit/internal/toolchain"
	auditext "github.com/chainreactors/cyber/audit/pkg/exts/audit"
	"github.com/chainreactors/cyber/core/eventbus"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/namespaces"
	"github.com/chainreactors/cyber/core/proc"
	cfg "github.com/chainreactors/cyber/pkg/config"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	arsenalext "github.com/chainreactors/cyber/pkg/exts/arsenal"
	nodeext "github.com/chainreactors/cyber/pkg/exts/node"
	okfext "github.com/chainreactors/cyber/pkg/exts/okf"
	protonext "github.com/chainreactors/cyber/pkg/exts/proton"
	ptyext "github.com/chainreactors/cyber/pkg/exts/pty"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	subagentext "github.com/chainreactors/cyber/pkg/exts/subagent"
	telemetryext "github.com/chainreactors/cyber/pkg/exts/telemetry"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	tuiext "github.com/chainreactors/cyber/pkg/exts/tui"
	harness "github.com/chainreactors/cyber/pkg/harness"
	nodepkg "github.com/chainreactors/cyber/pkg/node"
	"github.com/chainreactors/cyber/pkg/profile"
)

type auditProfile struct {
	extensions *extension.Set
	providers  *provider.State
	events     *coreevents.Stream
	progress   *eventbus.Bus[*toolpb.Progress]
	processes  *proc.Manager
	runtime    *agentsession.Runtime
	bindings   *consoleapi.Registry
	namespaces *namespaces.Registry
}

func newAuditProfile(request profile.Request, workDir string, bashTimeout int, report *runReport, manager *crtm.Manager, tools []toolchain.Status) (*auditProfile, error) {
	if request.Option == nil {
		return nil, fmt.Errorf("audit profile requires options")
	}
	if request.Session == nil {
		return nil, fmt.Errorf("audit profile requires a session")
	}
	option := *request.Option
	sessionConfig := *request.Session
	sessionConfig.NodeName = cfg.ResolveNodeName(option.NodeName)
	sessionConfig = sessionext.ConfigFromOption(&option, sessionConfig)
	loop := loopext.New(sessionConfig.Loop)
	var rgConfig, exclusions string
	if report != nil {
		var err error
		rgConfig, exclusions, err = report.searchExclusions()
		if err != nil {
			return nil, err
		}
	} else {
		exclusions = "!.git/**\n!.cyber/**"
	}
	reportDir := ""
	if report != nil {
		reportDir = report.Directory
	}
	var summary strings.Builder
	for _, tool := range tools {
		fmt.Fprintf(&summary, "%s %s (%s)\n", tool.Name, tool.Version, tool.Path)
	}
	environment := map[string]string{"PATH": manager.BinPath() + string(os.PathListSeparator) + os.Getenv("PATH")}
	if rgConfig != "" {
		environment["RIPGREP_CONFIG_PATH"] = rgConfig
	}

	// This build routes nothing, so it publishes the disabled endpoint and
	// links no proxy at all.
	values, err := harness.BaseExtensions(harness.BaseConfig{
		Directory:  workDir,
		SkillPaths: cfg.LocalSkillPaths(option.Skills),
		Terminal:   terminalext.Config{Timeout: bashTimeout, Environment: environment},
		Provider: provider.StartupConfig{
			Mode: request.ProviderMode, Config: cfg.ProviderConfig(&option),
			Fallbacks: cfg.FallbackProviderConfigs(&option),
		},
		Logger: request.Logger,
	})
	if err != nil {
		return nil, err
	}

	if report != nil {
		recorder, err := telemetryext.New(telemetryext.Options{Path: filepath.Join(report.Directory, "session.jsonl")})
		if err != nil {
			return nil, err
		}
		values = append(values, recorder)
	}
	values = append(values,
		arsenalext.New(manager), okfext.New(),
		protonext.New(protonext.Config{Directory: workDir, ExcludePaths: []string{filepath.Join(workDir, ".cyber"), filepath.Join(workDir, ".git")}}),
		auditext.New(auditext.Config{Workspace: workDir, ReportDir: reportDir, ToolSummary: summary.String(), SearchExclusions: exclusions}),
		loop, subagentext.New(), nodeext.New(),
	)
	values = append(values, ptyext.New())
	values = append(values,
		sessionext.New(sessionConfig), subagentext.NewTools(),
	)
	values = append(values, sessionext.NewProtocol())
	values = append(values, tuiext.New(), sessionext.NewConsole())
	p := &auditProfile{}
	values = append(values, extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		if p.providers, err = extension.Use[*provider.State](scope); err != nil {
			return err
		}
		if p.events, err = extension.Use[*coreevents.Stream](scope); err != nil {
			return err
		}
		if p.progress, err = extension.Use[*eventbus.Bus[*toolpb.Progress]](scope); err != nil {
			return err
		}
		if p.processes, err = extension.Use[*proc.Manager](scope); err != nil {
			return err
		}
		if p.runtime, err = extension.Use[*agentsession.Runtime](scope); err != nil {
			return err
		}
		if p.bindings, err = extension.Use[*consoleapi.Registry](scope); err != nil {
			return err
		}
		p.namespaces, err = extension.Use[*namespaces.Registry](scope)
		return err
	}})
	p.extensions, err = extension.New(values...)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (p *auditProfile) Load(ctx context.Context) error {
	if p == nil || p.extensions == nil {
		return fmt.Errorf("agent profile is unavailable")
	}
	return p.extensions.Load(ctx)
}

func (p *auditProfile) Close(ctx context.Context) error {
	if p == nil || p.extensions == nil {
		return nil
	}
	return p.extensions.Close(ctx)
}

// ConsoleBindings publishes the presentation contributions this profile
// assembled. It is nil until the graph is active.
func (p *auditProfile) ConsoleBindings() *consoleapi.Bindings {
	if p == nil || p.extensions == nil || !p.extensions.Active() || p.bindings == nil {
		return nil
	}
	return p.bindings.Bindings()
}

// Runtime is the session runtime this profile assembled.
func (p *auditProfile) Runtime() (*agentsession.Runtime, error) {
	if p == nil || p.extensions == nil || !p.extensions.Active() || p.runtime == nil {
		return nil, fmt.Errorf("agent profile is not active")
	}
	return p.runtime, nil
}

func (p *auditProfile) Active() bool {
	return p != nil && p.extensions != nil && p.extensions.Active()
}

func (p *auditProfile) Providers() (*provider.State, error) {
	if !p.Active() {
		return nil, fmt.Errorf("agent profile is not active")
	}
	return p.providers, nil
}

func (p *auditProfile) Events() (*coreevents.Stream, error) {
	if !p.Active() {
		return nil, fmt.Errorf("agent profile is not active")
	}
	return p.events, nil
}

func (p *auditProfile) Progress() (*eventbus.Bus[*toolpb.Progress], error) {
	if !p.Active() {
		return nil, fmt.Errorf("agent profile is not active")
	}
	return p.progress, nil
}

func (p *auditProfile) Processes() (*proc.Manager, error) {
	if !p.Active() {
		return nil, fmt.Errorf("agent profile is not active")
	}
	return p.processes, nil
}

func (p *auditProfile) RegisterNamespaces(mux *aop.NamespaceMux) error {
	if !p.Active() || p.namespaces == nil {
		return fmt.Errorf("agent profile has no active namespace registry")
	}
	return p.namespaces.Bind(mux)
}

func (p *auditProfile) AgentStatus() *aop.AgentStatus {
	if !p.Active() {
		return &aop.AgentStatus{}
	}
	return nodepkg.AgentStatus(p.providers)
}

var _ profile.Profile = (*auditProfile)(nil)
