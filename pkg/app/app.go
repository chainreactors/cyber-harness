package app

import (
	"context"
	"fmt"
	"os"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/provider"
	"github.com/chainreactors/aiscan/pkg/scanner/engines"
	"github.com/chainreactors/aiscan/pkg/scanner/resources"
	"github.com/chainreactors/aiscan/pkg/scanner/scan"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/skills"
	acpclient "github.com/chainreactors/ioa/client"
)

type Config struct {
	Provider ProviderConfig
	Vision   ProviderConfig
	Scanner  ScannerConfig
	Tools    ToolConfig
	ACP      *ACPConfig
	Logger   telemetry.Logger
}

type ProviderConfig struct {
	Enabled  bool
	Config   provider.ProviderConfig
	Optional bool
}

type ScannerConfig struct {
	CyberhubURL         string
	CyberhubKey         string
	CyberhubMode        string
	VerificationEnabled bool
	VerifyMinPriority   string
	VerifyTimeout       int
	VerifySystemPrompt  string
}

type ToolConfig struct {
	Enabled       bool
	BashTimeout   int
	VisionEnabled bool
}

type ACPConfig struct {
	URL           string
	NodeID        string
	NodeName      string
	RegisterTools bool
	AutoRegister  bool
	NodeMeta      map[string]any
}

type App struct {
	Provider         provider.Provider
	ProviderConfig   provider.ProviderConfig
	VisionConfig     provider.ProviderConfig
	Commands         *command.CommandRegistry
	Engines          *engines.Set
	Skills           *skills.Store
	SkillDiagnostics []skills.Diagnostic
	ACPClient        acpclient.API
	ACPStreamClient  acpclient.StreamAPI
}

func New(ctx context.Context, cfg Config) (*App, error) {
	app := &App{}
	logger := cfg.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}

	store, diagnostics := skills.LoadEmbeddedStore()
	app.Skills = store
	app.SkillDiagnostics = diagnostics

	if cfg.Provider.Enabled {
		llmProvider, resolved, err := initProvider(cfg.Provider.Config, logger)
		if err != nil {
			if !cfg.Provider.Optional {
				return nil, err
			}
			logger.Debugf("provider not configured: %s", err)
		} else {
			app.Provider = llmProvider
			app.ProviderConfig = *resolved
		}
	}

	app.Engines = initEngines(ctx, cfg.Scanner, logger)
	app.Commands = initCommandRegistry(app.Engines, cfg.Scanner, cfg.Tools, app.Provider, app.ProviderConfig.Model, app.Skills, logger)

	if cfg.ACP != nil {
		if err := app.InitACP(ctx, *cfg.ACP); err != nil {
			app.Close()
			return nil, err
		}
	}

	return app, nil
}

func (a *App) Close() {
	if a == nil {
		return
	}
	if a.Commands != nil {
		for _, t := range a.Commands.Tools() {
			if closer, ok := t.(interface{ Close() }); ok {
				closer.Close()
			}
		}
	}
	if a.Engines != nil {
		a.Engines.Close()
	}
}

func initProvider(cfg provider.ProviderConfig, logger telemetry.Logger) (provider.Provider, *provider.ProviderConfig, error) {
	resolved, err := provider.Resolve(&cfg)
	if err != nil {
		return nil, nil, err
	}
	logger.Infof("provider init provider=%s model=%s", resolved.Provider, resolved.Model)
	llmProvider, err := provider.NewProviderFromResolved(resolved)
	if err != nil {
		return nil, nil, err
	}
	return llmProvider, resolved, nil
}

func initEngines(ctx context.Context, cfg ScannerConfig, logger telemetry.Logger) *engines.Set {
	engineSet, err := engines.InitWithOptions(ctx, resources.Options{
		CyberhubURL: cfg.CyberhubURL,
		APIKey:      cfg.CyberhubKey,
		Mode:        cfg.CyberhubMode,
	}, logger)
	if err != nil {
		logger.Warnf("scanner engines init error=%q action=continue_without_scanners", err)
		return nil
	}
	return engineSet
}

func initCommandRegistry(engineSet *engines.Set, scanCfg ScannerConfig, toolCfg ToolConfig, llmProvider provider.Provider, model string, skillStore *skills.Store, logger telemetry.Logger) *command.CommandRegistry {
	cmdReg := command.NewRegistry()

	var scanOpts []any
	if scanCfg.VerificationEnabled && llmProvider != nil {
		p := llmProvider
		scanOpts = append(scanOpts, scan.WithVerifyFunc(func(ctx context.Context, prompt, systemPrompt, model string, maxTokens int) (string, error) {
			return agent.Run(ctx, prompt, command.NewRegistry(),
				agent.WithProvider(p),
				agent.WithModel(model),
				agent.WithMaxTokens(maxTokens),
				agent.WithSystemPrompt(systemPrompt),
				agent.WithLogger(telemetry.NopLogger()),
			)
		}))
		scanOpts = append(scanOpts, scan.WithVerificationConfig(scanVerificationConfig(scanCfg, model)))
	}
	scanOpts = append(scanOpts, scan.WithLogger(logger))

	deps := &command.Deps{
		EngineSet: engineSet,
		ScanOpts:  scanOpts,
		Logger:    logger,
		Model:     model,
	}
	if engineSet != nil {
		deps.Resources = engineSet.Resources
	}

	for _, gc := range command.BuildAll(deps) {
		cmdReg.Register(gc.Cmd, gc.Group)
	}

	workDir, _ := os.Getwd()
	timeout := toolCfg.BashTimeout
	if timeout <= 0 {
		timeout = 300
	}
	cmdReg.RegisterTool(command.NewReadTool(workDir, skillStore))
	cmdReg.RegisterTool(command.NewWriteTool(workDir))
	cmdReg.RegisterTool(command.NewGlobTool(workDir))
	cmdReg.RegisterTool(command.NewBashTool(workDir, timeout, cmdReg))

	logger.Infof("commands=%s", fmt.Sprintf("%v", cmdReg.Names()))
	return cmdReg
}

func (a *App) InitACP(ctx context.Context, cfg ACPConfig) error {
	client, err := newACPClient(cfg)
	if err != nil {
		return err
	}
	a.ACPClient = client
	if streamClient, ok := client.(acpclient.StreamAPI); ok {
		a.ACPStreamClient = streamClient
	}
	if cfg.RegisterTools && a.Commands != nil {
		deps := &command.Deps{
			ACPClient: client,
			NodeName:  cfg.NodeName,
			NodeMeta:  cfg.NodeMeta,
		}
		for _, cmd := range command.BuildGroup("ioa", deps) {
			a.Commands.Register(cmd, "ioa")
		}
	}
	if cfg.AutoRegister && client != nil && client.NodeID() == "" {
		_, err := client.RegisterNode(ctx, cfg.NodeName, cfg.NodeMeta)
		if err != nil {
			return err
		}
	}
	return nil
}

func newACPClient(cfg ACPConfig) (acpclient.API, error) {
	if cfg.URL == "" {
		return nil, nil
	}
	return acpclient.NewClient(cfg.URL, cfg.NodeID)
}

func scanVerificationConfig(cfg ScannerConfig, model string) scan.VerificationConfig {
	timeout := cfg.VerifyTimeout
	if timeout <= 0 {
		timeout = 120
	}
	return scan.VerificationConfig{
		Model:        model,
		Enable:       cfg.VerificationEnabled,
		MinPriority:  cfg.VerifyMinPriority,
		Timeout:      timeout,
		SystemPrompt: cfg.VerifySystemPrompt,
	}
}
