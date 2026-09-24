package scanner

import (
	"context"
	"fmt"
	"io/fs"

	curltools "github.com/chainreactors/cyber/tools/curl"
	gotools "github.com/chainreactors/cyber/tools/gogo"
	neutrontools "github.com/chainreactors/cyber/tools/neutron"
	"github.com/chainreactors/cyber/tools/scan"
	searchtools "github.com/chainreactors/cyber/tools/search"
	spraytools "github.com/chainreactors/cyber/tools/spray"
	zombietools "github.com/chainreactors/cyber/tools/zombie"
	"github.com/chainreactors/sdk/pkg/association"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/agent/subagent"
	"github.com/chainreactors/cyber/core/egress"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"

	scannerskills "github.com/chainreactors/cyber/pkg/exts/scanner/skills"
	"github.com/chainreactors/cyber/tools/files"
	"github.com/chainreactors/cyber/tools/resources"
	"github.com/chainreactors/cyber/tools/scan/engine"
	"github.com/chainreactors/utils/parsers"
)

// Config contains only scanner inputs selected by the Profile.
type Config struct {
	Resources resources.Options
	Recon     engine.ReconOptions
	// AgentName tags the scanner worker's AOP events; the composition root
	// sets it (aiscan passes "cyber").
	AgentName string
	Verify    string
}

type Extension struct {
	config        Config
	workDir       string
	engines       *engine.Set
	executionOnly bool
	files         *files.Files
}

func New(config Config, workDir string) *Extension {
	return &Extension{config: config, workDir: workDir}
}

// NewExecution installs scanner commands without borrowing model, prompt, skills,
// or agent-loop resources. Engine ownership and command construction are shared
// with the AI-enhanced distribution.
func NewExecution(config Config, workDir string) *Extension {
	return &Extension{config: config, workDir: workDir, executionOnly: true}
}

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || scope == nil {
		return fmt.Errorf("scanner extension is not configured")
	}
	logger, err := extension.Use[telemetry.Logger](scope)
	if err != nil {
		return err
	}
	endpoint, err := extension.Use[egress.Endpoint](scope)
	if err != nil {
		return err
	}
	stream, err := extension.Use[*events.Stream](scope)
	if err != nil {
		return err
	}
	if err := scan.ValidateVerify(e.config.Verify); err != nil {
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

	var options []scan.Option
	if !e.executionOnly {
		providers, err := extension.Use[*provider.State](scope)
		if err != nil {
			return err
		}
		tools, err := extension.Use[coretool.Executor](scope)
		if err != nil {
			return err
		}
		store, err := extension.Use[*skills.Store](scope)
		if err != nil {
			return err
		}
		hookRegistry, err := extension.Use[*hooks.Registry](scope)
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
		if err := extension.Add(scope, scannerPromptContribution(), cyberPromptContribution()); err != nil {
			return err
		}
		bundle, bundleDiags := scannerskills.Bundle()
		for _, diag := range bundleDiags {
			logger.Warnf("scanner skills: %s %s", diag.Path, diag.Message)
		}
		if err := extension.Add(scope, bundle); err != nil {
			return err
		}
		// Mount the owned skill tree so the read tool resolves the command
		// DescriptionPaths; the store bundle above serves skill invocations.
		filesCap, err := extension.Use[*files.Files](scope)
		if err != nil {
			return err
		}
		e.files = filesCap
		for _, dir := range []string{"cyber", "scan"} {
			sub, err := fs.Sub(scannerskills.FS(), dir)
			if err != nil {
				return err
			}
			if err := filesCap.Mount("cyber://skills/"+dir+"/", sub); err != nil {
				return err
			}
		}
		executor, err := extension.Use[subagent.Executor](scope)
		if err != nil {
			return err
		}
		readSkill := func(name string) string {
			content, ok, err := store.ReadVirtual("cyber://skills/scan/" + name + ".md")
			if !ok || err != nil {
				return ""
			}
			return content
		}
		if err := extension.Add(scope, scannerSubagents(readSkill)...); err != nil {
			return err
		}
		model, providerConfig := providers.Current()
		config := agent.Config{
			AgentName:      e.config.AgentName,
			Lifetime:       scope.Lifetime(),
			Hooks:          hookRegistry,
			Loop:           loop,
			Provider:       model,
			Tools:          tools,
			Model:          providerConfig.Model,
			MaxTokens:      providerConfig.MaxTokens,
			ContextWindow:  providerConfig.ContextWindow,
			Logger:         logger,
			Bus:            stream,
			PromptResolver: promptResolver,
		}
		currentConfig := func(ctx context.Context) agent.Config {
			if caller, ok := agent.ToolAgentConfig(ctx); ok {
				return caller
			}
			current := config
			model, pc := providers.Current()
			current.Provider = model
			current.Model, current.MaxTokens, current.ContextWindow = pc.Model, pc.MaxTokens, pc.ContextWindow
			return current
		}
		options = append(options,
			scan.WithVerification(e.config.Verify, func(ctx context.Context) bool { return currentConfig(ctx).Provider != nil }),
			scan.WithWorker(func(ctx context.Context, name string, loot parsers.Loot) (string, error) {
				return scannerWorker(executor, currentConfig(ctx))(ctx, name, loot)
			}),
		)
	} else {
		options = append(options, scan.WithExecutionOnly(), scan.WithVerification(e.config.Verify, nil))
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
		DescriptionPath: "cyber://skills/runtime/search.md",
		Run:             cyberhub.Run,
	})
	if command, err := newScanCommand(e.engines, options, proxyURL, stream); err != nil {
		logger.Warnf("scan unavailable: %v", err)
	} else {
		values = append(values, command)
	}
	values = append(values, manifestScannerCommands(stream, e.engines, logger, proxyURL)...)
	if err := extension.Add(scope, values...); err != nil {
		return err
	}
	return extension.Provide[*Availability](scope, availability(values, e.engines))
}

func (e *Extension) Close(ctx context.Context) error {
	if e == nil {
		return nil
	}
	if e.files != nil {
		for _, dir := range []string{"scan", "cyber"} {
			if err := e.files.Unmount(ctx, "cyber://skills/"+dir+"/"); err != nil {
				return err
			}
		}
		e.files = nil
	}
	if e.engines != nil {
		e.engines.Close()
		e.engines = nil
	}
	return nil
}

var _ extension.Extension = (*Extension)(nil)
