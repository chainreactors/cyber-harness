package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/provider"
	"github.com/chainreactors/aiscan/pkg/resources"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/scan"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/chainreactors/aiscan/skills"
	ioaclient "github.com/chainreactors/ioa/client"

	// Register scanner command factories with the unified command registry.
	_ "github.com/chainreactors/aiscan/pkg/tools"
)

type Config struct {
	Provider ProviderConfig
	Vision   ProviderConfig
	Scanner  ScannerConfig
	Tools    ToolConfig
	IOA      *IOAConfig
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

type IOAConfig struct {
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
	Engines          *engine.Set
	Skills           *skills.Store
	SkillDiagnostics []skills.Diagnostic
	IOAClient        ioaclient.API
	IOAStreamClient  ioaclient.StreamAPI
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

	// Resolve vision provider config for the vision pseudo-command.
	var visionCfg *provider.ProviderConfig
	if cfg.Tools.Enabled && cfg.Tools.VisionEnabled && cfg.Vision.Enabled {
		resolved, err := provider.Resolve(&cfg.Vision.Config)
		if err != nil {
			if !cfg.Vision.Optional {
				return nil, fmt.Errorf("vision provider: %w", err)
			}
			logger.Debugf("vision provider not configured: %s", err)
		} else {
			app.VisionConfig = *resolved
			visionCfg = &app.VisionConfig
		}
	} else if cfg.Tools.Enabled && cfg.Tools.VisionEnabled && app.Provider != nil {
		visionCfg = &app.ProviderConfig
	}

	app.Commands = initCommandRegistry(app.Engines, cfg.Scanner, cfg.Tools, app.Provider, app.ProviderConfig.Model, app.Skills, visionCfg, logger)

	if cfg.IOA != nil {
		if err := app.InitIOA(ctx, *cfg.IOA); err != nil {
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
		for _, cmd := range a.Commands.All() {
			if closer, ok := cmd.(interface{ Close() }); ok {
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

func initEngines(ctx context.Context, cfg ScannerConfig, logger telemetry.Logger) *engine.Set {
	engineSet, err := engine.InitWithOptions(ctx, resources.Options{
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

func initCommandRegistry(engineSet *engine.Set, scanCfg ScannerConfig, toolCfg ToolConfig, llmProvider provider.Provider, model string, skillStore *skills.Store, visionCfg *provider.ProviderConfig, logger telemetry.Logger) *command.CommandRegistry {
	cmdReg := command.NewRegistry()

	workDir, _ := os.Getwd()

	var scanOpts []any
	if scanCfg.VerificationEnabled && llmProvider != nil {
		p := llmProvider
		scanOpts = append(scanOpts, scan.WithVerifyFunc(func(ctx context.Context, prompt, systemPrompt, model string, maxTokens int) (string, error) {
			return agent.Run(ctx, prompt, cmdReg,
				agent.WithProvider(p),
				agent.WithModel(model),
				agent.WithMaxTokens(maxTokens),
				agent.WithSystemPrompt(buildScanVerifySystemPrompt(cmdReg, skillStore, systemPrompt)),
				agent.WithBeforeToolCall(scanVerifyBeforeToolCall),
				agent.WithLogger(logger),
			)
		}))
		scanOpts = append(scanOpts, scan.WithVerificationConfig(scanVerificationConfig(scanCfg, model)))
	}
	scanOpts = append(scanOpts, scan.WithLogger(logger))

	deps := &command.Deps{
		WorkDir:      workDir,
		BashTimeout:  toolCfg.BashTimeout,
		SkillStore:   skillStore,
		EngineSet:    engineSet,
		VisionConfig: visionCfg,
		ScanOpts:     scanOpts,
		Logger:       logger,
		Model:        model,
	}
	if engineSet != nil {
		deps.Resources = engineSet.Resources
	}

	command.BuildAll(deps, cmdReg)

	logger.Infof("commands=%s", fmt.Sprintf("%v", cmdReg.Names()))
	return cmdReg
}

func buildScanVerifySystemPrompt(cmdReg *command.CommandRegistry, skillStore *skills.Store, verificationPrompt string) string {
	preamble := strings.TrimSpace(verificationPrompt)
	if preamble == "" {
		preamble = "You are aiscan's scan verification agent."
	}
	preamble += `

You are running inside the scan pipeline to verify one finding. Use the existing agent tools and pseudo-commands when they help, especially web_search and web_fetch via the bash tool for historical vulnerability context. Do not run scanner pseudo-commands such as scan, gogo, spray, zombie, or neutron from this verification step. Do not perform destructive or exploitative actions.

Return only the exact status:/summary:/evidence: lines requested by the user prompt. Mark status confirmed only when evidence supports the finding; otherwise return not_confirmed or inconclusive.`

	var skillList []skills.Skill
	if skillStore != nil {
		skillList = skillStore.Skills
	}
	scannerDocs := ""
	if cmdReg != nil {
		scannerDocs = cmdReg.UsageDocs()
	}
	return agent.BuildSystemPrompt(&agent.PromptConfig{
		Tools:          cmdReg,
		ScannerDocs:    scannerDocs,
		CustomPreamble: preamble,
		Skills:         skillList,
	})
}

func scanVerifyBeforeToolCall(_ context.Context, call agent.BeforeToolCallContext) (*agent.BeforeToolCallResult, error) {
	if call.ToolCall.Function.Name != "bash" {
		return nil, nil
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(call.ToolCall.Function.Arguments), &args); err != nil {
		return nil, nil
	}
	if !scanVerifyBlocksCommand(args.Command) {
		return nil, nil
	}
	return &agent.BeforeToolCallResult{
		Block:  true,
		Reason: "scan verification may use web_search/web_fetch, but scanner pseudo-commands are blocked to avoid recursive or active scanning",
	}, nil
}

func scanVerifyBlocksCommand(commandLine string) bool {
	tokens, err := command.SplitCommandLine(commandLine)
	if err != nil {
		tokens = strings.Fields(commandLine)
	}
	if len(tokens) == 0 {
		return false
	}
	if isScanVerifyBlockedCommand(tokens[0]) {
		return true
	}
	return strings.EqualFold(tokens[0], "aiscan") && len(tokens) > 1 && isScanVerifyBlockedCommand(tokens[1])
}

func isScanVerifyBlockedCommand(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "scan", "gogo", "spray", "zombie", "neutron":
		return true
	default:
		return false
	}
}

func (a *App) InitIOA(ctx context.Context, cfg IOAConfig) error {
	client, err := newIOAClient(cfg)
	if err != nil {
		return err
	}
	a.IOAClient = client
	if streamClient, ok := client.(ioaclient.StreamAPI); ok {
		a.IOAStreamClient = streamClient
	}
	if cfg.RegisterTools && a.Commands != nil {
		deps := &command.Deps{
			IOAClient: client,
			NodeName:  cfg.NodeName,
			NodeMeta:  cfg.NodeMeta,
		}
		command.BuildGroup("ioa", deps, a.Commands)
	}
	if cfg.AutoRegister && client != nil && client.NodeID() == "" {
		_, err := client.RegisterNode(ctx, cfg.NodeName, cfg.NodeMeta)
		if err != nil {
			return err
		}
	}
	return nil
}

func newIOAClient(cfg IOAConfig) (ioaclient.API, error) {
	if cfg.URL == "" {
		return nil, nil
	}
	return ioaclient.NewClient(cfg.URL, cfg.NodeID)
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
