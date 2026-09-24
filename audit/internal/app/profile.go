package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	crtm "github.com/chainreactors/crtm/pkg"
	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	auditext "github.com/chainreactors/cyber/audit/pkg/exts/audit"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	cfg "github.com/chainreactors/cyber/pkg/config"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	arsenalext "github.com/chainreactors/cyber/pkg/exts/arsenal"
	okfext "github.com/chainreactors/cyber/pkg/exts/okf"
	protonext "github.com/chainreactors/cyber/pkg/exts/proton"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	subagentext "github.com/chainreactors/cyber/pkg/exts/subagent"
	telemetryext "github.com/chainreactors/cyber/pkg/exts/telemetry"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	tuiext "github.com/chainreactors/cyber/pkg/exts/tui"
	harness "github.com/chainreactors/cyber/pkg/harness"
)

type auditProfile struct {
	extensions *extension.Set
	runtime    *agentsession.Runtime
	bindings   *consoleapi.Registry
}

func newAuditProfile(option cfg.Option, logger telemetry.Logger, workDir string, bashTimeout int, report *runReport, manager *crtm.Manager) (*auditProfile, error) {
	sessionConfig := sessionext.ConfigFromOption(&option, agentsession.Config{
		NodeName:         cfg.ResolveNodeName(option.NodeName),
		PrimarySessionID: "main", Loop: agent.StandardLoop{},
	})
	loop := loopext.New(sessionConfig.Loop)
	recorder, err := telemetryext.New(telemetryext.Options{Path: filepath.Join(report.Directory, "session.jsonl")})
	if err != nil {
		return nil, err
	}
	rgConfig, exclusions, err := report.searchExclusions()
	if err != nil {
		return nil, err
	}
	var summary strings.Builder
	for _, tool := range report.Tools {
		fmt.Fprintf(&summary, "%s %s (%s)\n", tool.Name, tool.Version, tool.Path)
	}
	environment := map[string]string{"PATH": manager.BinPath() + string(os.PathListSeparator) + os.Getenv("PATH"), "RIPGREP_CONFIG_PATH": rgConfig}

	// This build routes nothing, so it publishes the disabled endpoint and
	// links no proxy at all.
	values, err := harness.BaseExtensions(harness.BaseConfig{
		Directory:  workDir,
		SkillPaths: agentSkillPaths(option.Skills),
		Terminal:   terminalext.Config{Timeout: bashTimeout, Environment: environment},
		Provider: provider.StartupConfig{
			Mode: provider.StartupRequired, Config: cfg.ProviderConfig(&option),
			Fallbacks: cfg.FallbackProviderConfigs(&option),
		},
		Logger: logger,
	})
	if err != nil {
		return nil, err
	}

	p := &auditProfile{}
	values = append(values,
		recorder, arsenalext.New(manager), okfext.New(),
		protonext.New(protonext.Config{Directory: workDir, ExcludePaths: []string{report.Directory, filepath.Join(workDir, ".cyber"), filepath.Join(workDir, ".git")}}),
		auditext.New(auditext.Config{Workspace: workDir, ReportDir: report.Directory, ToolSummary: summary.String(), SearchExclusions: exclusions}),
		loop,
		subagentext.New(), sessionext.New(sessionConfig), subagentext.NewTools(),
		tuiext.New(),
		sessionext.NewConsole(),
		extension.Func{LoadFunc: func(scope *extension.Scope) error {
			var err error
			if p.runtime, err = extension.Use[*agentsession.Runtime](scope); err != nil {
				return err
			}
			p.bindings, err = extension.Use[*consoleapi.Registry](scope)
			return err
		}},
	)
	set, err := extension.New(values...)
	if err != nil {
		return nil, err
	}
	p.extensions = set
	return p, nil
}

func agentSkillPaths(values []string) []string {
	var paths []string
	for _, value := range values {
		if strings.ContainsAny(value, `/\`) || strings.HasPrefix(value, ".") {
			paths = append(paths, value)
		}
	}
	return paths
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
