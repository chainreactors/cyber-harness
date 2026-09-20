package scanner

import (
	"context"
	"fmt"

	curltools "github.com/chainreactors/cyber/tools/curl"
	gotools "github.com/chainreactors/cyber/tools/gogo"
	neutrontools "github.com/chainreactors/cyber/tools/neutron"
	protontools "github.com/chainreactors/cyber/tools/proton"
	"github.com/chainreactors/cyber/tools/scan"
	searchtools "github.com/chainreactors/cyber/tools/search"
	spraytools "github.com/chainreactors/cyber/tools/spray"
	zombietools "github.com/chainreactors/cyber/tools/zombie"
	"github.com/chainreactors/sdk/pkg/association"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/core/egress"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"

	"github.com/chainreactors/cyber/tools/resources"
	"github.com/chainreactors/cyber/tools/scan/engine"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
)

// Config contains only scanner inputs selected by the Profile.
type Config struct {
	Resources resources.Options
	Recon     engine.ReconOptions
}

type Extension struct {
	config  Config
	workDir string
	engines *engine.Set
}

func New(config Config, workDir string) *Extension {
	return &Extension{config: config, workDir: workDir}
}

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || scope == nil {
		return fmt.Errorf("scanner extension is not configured")
	}
	logger, err := extension.Use[*telemetry.LoggerRef](scope)
	if err != nil {
		return err
	}
	providers, err := extension.Use[*provider.State](scope)
	if err != nil {
		return err
	}
	endpoint, err := extension.Use[egress.Endpoint](scope)
	if err != nil {
		return err
	}
	tools, err := extension.Use[coretool.Executor](scope)
	if err != nil {
		return err
	}
	commandRegistry, err := extension.Use[coretool.CommandExecutor](scope)
	if err != nil {
		return err
	}
	bash, err := extension.Use[*terminaltool.BashTool](scope)
	if err != nil {
		return err
	}
	store, err := extension.Use[*skills.Store](scope)
	if err != nil {
		return err
	}
	loop, err := extension.Use[agent.Loop](scope)
	if err != nil {
		return err
	}
	promptResolver, err := extension.Use[prompt.Resolver](scope)
	if err != nil {
		return err
	}
	if err := extension.Add(scope, scannerPromptContribution()); err != nil {
		return err
	}
	stream, err := extension.Use[*events.Stream](scope)
	if err != nil {
		return err
	}
	proxyURL := endpoint.ProxyURL()
	if proxyURL == "" {
		proxyURL = e.config.Resources.Proxy
	}
	e.engines, err = engine.InitWithOptions(scope.Init(), e.config.Resources, logger)
	if err != nil {
		logger.Warnf("scanner engines init error=%q action=continue_without_scanners", err)
		e.engines = nil
	} else {
		e.engines.SetupUncover(e.config.Recon, logger)
	}
	var scannerResources *resources.Set
	if e.engines != nil {
		scannerResources = e.engines.Resources
	}

	var options []scan.Option
	model, providerConfig := providers.Current()
	if model != nil {
		parent := agent.NewAgent(agent.Config{
			Loop:           loop,
			Provider:       model,
			Tools:          tools,
			Model:          providerConfig.Model,
			MaxTokens:      providerConfig.MaxTokens,
			ContextWindow:  providerConfig.ContextWindow,
			Logger:         logger,
			Bus:            stream,
			PromptResolver: promptResolver,
		})
		options = append(options,
			scan.WithParent(parent),
			scan.WithDeepBrowserFunc(func(ctx context.Context, targetURL string) (string, error) {
				return collectDeepBrowserArtifacts(ctx, commandRegistry, bash, targetURL, logger)
			}),
		)
		options = append(options, scan.WithSkillReader(func(name string) string {
			content, ok, err := store.ReadVirtual("cyber://skills/scan/" + name + ".md")
			if !ok || err != nil {
				return ""
			}
			return content
		}))
	}
	options = append(options, scan.WithLogger(logger))

	var values []coretool.Command
	values = append(values, curltools.NewCommand(logger, proxyURL, stream))
	if command, err := gotools.NewCommand(e.engines, logger, proxyURL, stream); err != nil {
		logger.Warnf("gogo unavailable: %v", err)
	} else {
		values = append(values, command)
	}
	if command, err := neutrontools.NewCommand(e.engines, logger, proxyURL, stream); err != nil {
		logger.Warnf("neutron unavailable: %v", err)
	} else {
		values = append(values, command)
	}
	if command, err := spraytools.NewCommand(e.engines, logger, proxyURL, stream); err != nil {
		logger.Warnf("spray unavailable: %v", err)
	} else {
		values = append(values, command)
	}
	if command, err := zombietools.NewCommand(e.engines, logger, proxyURL, stream); err != nil {
		logger.Warnf("zombie unavailable: %v", err)
	} else {
		values = append(values, command)
	}
	values = append(values, protontools.NewCommand(e.workDir, scannerResources, logger, proxyURL, stream))
	// cyberhub searches the fingerprint and POC index this extension builds, so
	// it is contributed by its owner. A nil index is not an empty one: the
	// command says how to configure the resources instead of reporting no hits.
	var index *association.Index
	if e.engines != nil {
		index = e.engines.Index
	}
	cyberhub := searchtools.NewCyberhubSearch(index)
	values = append(values, coretool.Command{
		Name: cyberhub.Name(), Usage: cyberhub.Usage(),
		DescriptionPath: "cyber://skills/cyber/okf/runtime/search.md",
		Run:             cyberhub.Run,
	})
	if command, err := newScanCommand(e.engines, options, proxyURL, stream); err != nil {
		logger.Warnf("scan unavailable: %v", err)
	} else {
		values = append(values, command)
	}
	values = append(values, manifestScannerCommands(stream, e.engines, logger, proxyURL)...)
	return extension.Add(scope, values...)
}

func (e *Extension) Close(context.Context) error {
	if e == nil {
		return nil
	}
	if e.engines != nil {
		e.engines.Close()
		e.engines = nil
	}
	return nil
}

var _ extension.Extension = (*Extension)(nil)
