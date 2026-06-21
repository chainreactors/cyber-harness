package cmd

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/runner"
	"github.com/chainreactors/aiscan/pkg/agent"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tui"
	"github.com/chainreactors/aiscan/skills"
)

type fakeConsoleProvider struct {
	requests int
}

func (p *fakeConsoleProvider) Name() string { return "fake" }

func (p *fakeConsoleProvider) ChatCompletion(_ context.Context, req *agent.ChatCompletionRequest) (*agent.ChatCompletionResponse, error) {
	p.requests++
	return &agent.ChatCompletionResponse{
		Choices: []agent.Choice{{
			Message: agent.NewTextMessage("assistant", "ok"),
		}},
	}, nil
}

func TestParseCLIScanExtractsLLMAndPassesScannerArgs(t *testing.T) {
	parsed, err := parseCLI([]string{
		"--cyberhub-url", "http://hub:8080",
		"scan",
		"-i", "127.0.0.1",
		"--verify=high",
		"--api-key", "KEY",
		"--model=deepseek-v4-pro",
		"--base-url", "https://api.deepseek.com",
		"--cyberhub-key=HUBKEY",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Mode != cfg.RunModeScanner {
		t.Fatalf("mode = %s, want %s", parsed.Mode, cfg.RunModeScanner)
	}
	wantArgs := []string{"scan", "-i", "127.0.0.1", "--verify=high"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
	opt := parsed.Option
	if opt.APIKey != "KEY" || opt.Model != "deepseek-v4-pro" || opt.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("llm options = %#v", opt.LLMOptions)
	}
	if opt.CyberhubURL != "http://hub:8080" || opt.CyberhubKey != "HUBKEY" {
		t.Fatalf("scanner options = %#v", opt.ScannerOptions)
	}
}

func TestParseCLIScannerDebugEnablesGlobalDebugAndPreservesArg(t *testing.T) {
	parsed, err := parseCLI([]string{"scan", "-i", "127.0.0.1", "--debug"})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if !parsed.Option.Debug {
		t.Fatal("scanner --debug should enable global debug")
	}
	wantArgs := []string{"scan", "-i", "127.0.0.1", "--debug"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
}

func TestDirectScannerModeSuppressesInitInfoByDefault(t *testing.T) {
	var logBuf bytes.Buffer
	logger := telemetry.NewLogger(telemetry.LogConfig{Output: &logBuf})
	err := runner.RunDirectScannerMode(context.Background(), &cfg.Option{
		MiscOptions: cfg.MiscOptions{NoColor: true},
	}, []string{"scan", "-i", "http://127.0.0.1:1", "--timeout", "1", "--no-color"}, logger)
	if err != nil {
		t.Fatalf("RunDirectScannerMode() error = %v", err)
	}
	logText := logBuf.String()
	for _, unwanted := range []string{"provider init", "engine=fingers", "resources type="} {
		if strings.Contains(logText, unwanted) {
			t.Fatalf("normal mode leaked init log %q:\n%s", unwanted, logText)
		}
	}
}

func TestDirectScannerModeDebugShowsInitInfo(t *testing.T) {
	var logBuf bytes.Buffer
	logger := telemetry.NewLogger(telemetry.LogConfig{Debug: true, Output: &logBuf})
	err := runner.RunDirectScannerMode(context.Background(), &cfg.Option{
		MiscOptions: cfg.MiscOptions{Debug: true, NoColor: true},
	}, []string{"scan", "-i", "http://127.0.0.1:1", "--timeout", "1", "--no-color"}, logger)
	if err != nil {
		t.Fatalf("RunDirectScannerMode() error = %v", err)
	}
	logText := logBuf.String()
	if !strings.Contains(logText, "engine=fingers status=ready") || !strings.Contains(logText, "scanner commands ready") {
		t.Fatalf("debug scanner logs missing init detail:\n%s", logText)
	}
}

func TestParseCLIAgentAcceptsLLMFlags(t *testing.T) {
	parsed, err := parseCLI([]string{
		"agent",
		"--base-url", "https://api.deepseek.com",
		"--api-key", "KEY",
		"--model", "deepseek-v4-pro",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Mode != cfg.RunModeAgent {
		t.Fatalf("mode = %s, want %s", parsed.Mode, cfg.RunModeAgent)
	}
	opt := parsed.Option
	if opt.BaseURL != "https://api.deepseek.com" || opt.APIKey != "KEY" || opt.Model != "deepseek-v4-pro" {
		t.Fatalf("llm options = %#v", opt.LLMOptions)
	}
	pcfg := cfg.ProviderConfig(&opt)
	if pcfg.Provider != "" {
		t.Fatalf("provider should be unresolved before agent.ResolveProvider, got %q", pcfg.Provider)
	}
	resolved, err := agent.ResolveProvider(&pcfg)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Provider != "openai" {
		t.Fatalf("resolved provider = %q, want openai (DeepSeek uses OpenAI-compatible protocol)", resolved.Provider)
	}
}

func TestParseCLIScanExtractsLLMFlags(t *testing.T) {
	parsed, err := parseCLI([]string{
		"scan",
		"-i", "127.0.0.1",
		"--base-url", "https://api.deepseek.com",
		"--api-key", "KEY",
		"--model", "deepseek-v4-pro",
		"--verify=high",
		"--sniper",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	wantArgs := []string{"scan", "-i", "127.0.0.1", "--verify=high", "--sniper"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
	opt := parsed.Option
	if opt.AI || opt.APIKey != "KEY" || opt.Model != "deepseek-v4-pro" || opt.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("llm options = %#v", opt.LLMOptions)
	}
	pcfg := cfg.ProviderConfig(&opt)
	if pcfg.Provider != "" {
		t.Fatalf("provider should be unresolved before agent.ResolveProvider, got %q", pcfg.Provider)
	}
	resolved, err := agent.ResolveProvider(&pcfg)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Provider != "openai" {
		t.Fatalf("resolved provider = %q, want openai (DeepSeek uses OpenAI-compatible protocol)", resolved.Provider)
	}
}

func TestParseCLIScanAcceptsPromptShortFlagAfterCommand(t *testing.T) {
	parsed, err := parseCLI([]string{
		"scan",
		"-i", "127.0.0.1",
		"-p", "review focus fingerprints",
		"--sniper",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Option.AI {
		t.Fatal("scan --sniper should not enable global --ai")
	}
	if parsed.Option.Prompt != "review focus fingerprints" {
		t.Fatalf("prompt = %q, want %q", parsed.Option.Prompt, "review focus fingerprints")
	}
	wantArgs := []string{"scan", "-i", "127.0.0.1", "--sniper"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
}

func TestParseCLIScanPostAIIsNotExtracted(t *testing.T) {
	parsed, err := parseCLI([]string{
		"scan",
		"-i", "127.0.0.1",
		"--ai",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Option.AI {
		t.Fatal("scan-local --ai should not enable global --ai")
	}
	wantArgs := []string{"scan", "-i", "127.0.0.1", "--ai"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
}

func TestParseCLIRootPromptValueCanMatchScannerName(t *testing.T) {
	parsed, err := parseCLI([]string{
		"-p", "gogo",
		"scan",
		"-i", "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Option.Prompt != "gogo" {
		t.Fatalf("prompt = %q, want gogo", parsed.Option.Prompt)
	}
	wantArgs := []string{"scan", "-i", "127.0.0.1"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
}

func TestParseCLICyberhubModeRootAndPassthrough(t *testing.T) {
	parsed, err := parseCLI([]string{
		"--cyberhub-mode", "override",
		"spray",
		"-u", "http://127.0.0.1:5000",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Option.CyberhubMode != "override" {
		t.Fatalf("cyberhub mode = %q, want override", parsed.Option.CyberhubMode)
	}
	if !reflect.DeepEqual(parsed.ScannerArgs, []string{"spray", "-u", "http://127.0.0.1:5000"}) {
		t.Fatalf("scanner args = %#v", parsed.ScannerArgs)
	}
}

func TestParseCLINonScanScannerKeepsPostRootArgsIsolated(t *testing.T) {
	parsed, err := parseCLI([]string{
		"gogo",
		"-i", "127.0.0.1",
		"--cyberhub-url", "http://hub:8080",
		"--cyberhub-key", "HUBKEY",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	wantArgs := []string{"gogo", "-i", "127.0.0.1", "--cyberhub-url", "http://hub:8080", "--cyberhub-key", "HUBKEY"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
	if parsed.Option.CyberhubURL != "" || parsed.Option.CyberhubKey != "" {
		t.Fatalf("scanner options = %#v", parsed.Option.ScannerOptions)
	}
}

func TestParseCLIScannerRootArgsBeforeCommandStillApply(t *testing.T) {
	parsed, err := parseCLI([]string{
		"--cyberhub-url", "http://hub:8080",
		"--cyberhub-key", "HUBKEY",
		"gogo",
		"-i", "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	wantArgs := []string{"gogo", "-i", "127.0.0.1"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
	if parsed.Option.CyberhubURL != "http://hub:8080" || parsed.Option.CyberhubKey != "HUBKEY" {
		t.Fatalf("scanner options = %#v", parsed.Option.ScannerOptions)
	}
}

func TestParseCLIGogoKeepsPortShortFlagAfterCommand(t *testing.T) {
	parsed, err := parseCLI([]string{
		"gogo",
		"-i", "127.0.0.1",
		"-p", "top100",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	wantArgs := []string{"gogo", "-i", "127.0.0.1", "-p", "top100"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
	if parsed.Option.Prompt != "" {
		t.Fatalf("prompt = %q, want empty", parsed.Option.Prompt)
	}
}

func TestParseCLINeutronKeepsConcurrencyShortFlagAfterCommand(t *testing.T) {
	parsed, err := parseCLI([]string{
		"neutron",
		"-u", "http://127.0.0.1",
		"-c", "10",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	wantArgs := []string{"neutron", "-u", "http://127.0.0.1", "-c", "10"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
	if parsed.Option.ConfigFile != "" {
		t.Fatalf("config file = %q, want empty", parsed.Option.ConfigFile)
	}
}

func TestParseCLINonScanScannerDoesNotExtractPostAIFlags(t *testing.T) {
	parsed, err := parseCLI([]string{
		"gogo",
		"-i", "127.0.0.1",
		"--ai",
		"--model", "deepseek-v4-pro",
		"--prompt", "review focus fingerprints",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	wantArgs := []string{"gogo", "-i", "127.0.0.1", "--ai", "--model", "deepseek-v4-pro", "--prompt", "review focus fingerprints"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
	if parsed.Option.AI || parsed.Option.Model != "" || parsed.Option.Prompt != "" {
		t.Fatalf("option = %#v", parsed.Option)
	}
}

func TestParseCLIPassthroughScannerExtractsAIIntentArgs(t *testing.T) {
	parsed, err := parseCLI([]string{
		"--api-key", "KEY",
		"--prompt", "review focus fingerprints",
		"--skill", "scan",
		"--ai",
		"--model", "deepseek-v4-pro",
		"--skill=aiscan",
		"gogo",
		"-i", "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Mode != cfg.RunModeScanner {
		t.Fatalf("mode = %s, want %s", parsed.Mode, cfg.RunModeScanner)
	}
	wantArgs := []string{"gogo", "-i", "127.0.0.1"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
	opt := parsed.Option
	if !opt.AI || opt.APIKey != "KEY" || opt.Model != "deepseek-v4-pro" || opt.Prompt != "review focus fingerprints" {
		t.Fatalf("option = %#v", opt)
	}
	if !reflect.DeepEqual(opt.Skills, []string{"scan", "aiscan"}) {
		t.Fatalf("skills = %#v", opt.Skills)
	}
}

func TestScannerAIIntentInjectsCommandSkill(t *testing.T) {
	store, diagnostics := skills.LoadEmbeddedStore()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	intent, err := cfg.ApplySelectedSkills("focus on risky exposed services", nil, store)
	if err != nil {
		t.Fatalf("ApplySelectedSkills() error = %v", err)
	}
	if !strings.Contains(intent, "focus on risky exposed services") {
		t.Fatalf("intent missing user text:\n%s", intent)
	}
}

func TestParseCLIAgentIOAFlag(t *testing.T) {
	parsed, err := parseCLI([]string{
		"--debug",
		"--cyberhub-mode", "override",
		"agent",
		"-p", "scan localhost",
		"-s", "aiscan",
		"--space", "case-1",
		"--heartbeat", "5",
		"--model", "gpt-4o",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Mode != cfg.RunModeAgent {
		t.Fatalf("mode = %s, want %s", parsed.Mode, cfg.RunModeAgent)
	}
	opt := parsed.Option
	if !opt.Debug || opt.Prompt != "scan localhost" || opt.Space != "case-1" || opt.Heartbeat != 5 || opt.Model != "gpt-4o" || opt.CyberhubMode != "override" {
		t.Fatalf("option = %#v", opt)
	}
	if !reflect.DeepEqual(opt.Skills, []string{"aiscan"}) {
		t.Fatalf("skills = %#v", opt.Skills)
	}
}

func TestParseCLIAgentWebURL(t *testing.T) {
	parsed, err := parseCLI([]string{
		"agent",
		"--web-url", "http://127.0.0.1:8080",
		"--ioa-url", "http://token@127.0.0.1:8080/ioa",
		"--space", "case-1",
		"--ioa-node-name", "worker-1",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Mode != cfg.RunModeAgent {
		t.Fatalf("mode = %s, want %s", parsed.Mode, cfg.RunModeAgent)
	}
	opt := parsed.Option
	if opt.WebURL != "http://127.0.0.1:8080" || opt.IOAURL != "http://token@127.0.0.1:8080/ioa" || opt.Space != "case-1" || opt.IOANodeName != "worker-1" {
		t.Fatalf("option = %#v", opt)
	}
}

func TestAgentConsoleArgsForLine(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantArgs []string
	}{
		{name: "empty", input: "  ", wantArgs: nil},
		{name: "prompt", input: " scan localhost ", wantArgs: []string{"__prompt", "scan localhost"}},
		{name: "quoted prompt is preserved", input: `explain "scan result"`, wantArgs: []string{"__prompt", `explain "scan result"`}},
		{name: "help", input: "/help", wantArgs: []string{"/help"}},
		{name: "reset", input: "/reset", wantArgs: []string{"/reset"}},
		{name: "continue", input: "/continue", wantArgs: []string{"/continue"}},
		{name: "exit", input: "/exit", wantArgs: []string{"/exit"}},
		{name: "quit", input: "/quit", wantArgs: []string{"/quit"}},
		{name: "skill slash command preserves prompt", input: `/scan explain "scan result"`, wantArgs: []string{"/scan", `explain "scan result"`}},
		{name: "unknown slash command", input: "/unknown", wantArgs: []string{"/unknown"}},
		{name: "legacy skill command", input: "/skill:scan check target", wantArgs: []string{"__prompt", "/skill:scan check target"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotArgs, err := tui.AgentConsoleArgsForLine(tt.input)
			if err != nil {
				t.Fatalf("AgentConsoleArgsForLine() error = %v", err)
			}
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Fatalf("AgentConsoleArgsForLine() = %#v, want %#v", gotArgs, tt.wantArgs)
			}
		})
	}
}

func TestAgentConsoleRegistersSkillsAsSlashCommands(t *testing.T) {
	store, diagnostics := skills.LoadEmbeddedStore()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	repl := tui.NewAgentConsole(context.Background(), &cfg.Option{}, tui.AppInfo{Skills: store}, nil, nil)
	_ = repl // console created successfully
}

func TestAgentConsolePromptCommandRunsAgent(t *testing.T) {
	store, diagnostics := skills.LoadEmbeddedStore()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	llm := &fakeConsoleProvider{}
	session := agent.NewAgent(agent.Config{Provider: llm, Tools: commands.NewRegistry()})
	repl := tui.NewAgentConsole(context.Background(), &cfg.Option{}, tui.AppInfo{Skills: store}, session, nil)
	_ = repl // console created successfully — full REPL test requires readline
}

func TestParseCLIIOAServeCommandUsesURL(t *testing.T) {
	parsed, err := parseCLI([]string{
		"ioa",
		"serve",
		"--ioa-url", "http://127.0.0.1:9999",
		"--timeout", "10",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Mode != cfg.RunModeIOAServe {
		t.Fatalf("mode = %s, want %s", parsed.Mode, cfg.RunModeIOAServe)
	}
	opt := parsed.Option
	if opt.IOAURL != "http://127.0.0.1:9999" || opt.Timeout != 10 {
		t.Fatalf("option = %#v", opt)
	}
}

func TestDirectScannerRuntimeFeaturesForVerifyModes(t *testing.T) {
	withDefaults(t, func() {
		cfg.DefaultVerify = "off"
		features, args, err := runner.DirectScannerRuntimeFeatures([]string{"scan", "-i", "127.0.0.1"})
		if err != nil {
			t.Fatalf("DirectScannerRuntimeFeatures() error = %v", err)
		}
		if features.ProviderEnabled || features.AIEnabled {
			t.Fatalf("features = %#v", features)
		}
		if !reflect.DeepEqual(args, []string{"scan", "-i", "127.0.0.1"}) {
			t.Fatalf("args = %#v", args)
		}

		features, args, err = runner.DirectScannerRuntimeFeatures([]string{"scan", "-i", "127.0.0.1", "--verify=off"})
		if err != nil {
			t.Fatalf("DirectScannerRuntimeFeatures() error = %v", err)
		}
		if features.ProviderEnabled || features.AIEnabled {
			t.Fatalf("features = %#v", features)
		}
		if !reflect.DeepEqual(args, []string{"scan", "-i", "127.0.0.1", "--verify=off"}) {
			t.Fatalf("args = %#v", args)
		}

		features, args, err = runner.DirectScannerRuntimeFeatures([]string{"scan", "-i", "127.0.0.1", "--deep"})
		if err != nil {
			t.Fatalf("DirectScannerRuntimeFeatures() error = %v", err)
		}
		if !features.ProviderEnabled || features.ProviderOptional || !features.AIEnabled || !features.ScannerAI {
			t.Fatalf("deep features = %#v", features)
		}
		if !reflect.DeepEqual(args, []string{"scan", "-i", "127.0.0.1", "--deep"}) {
			t.Fatalf("args = %#v", args)
		}

		features, _, err = runner.DirectScannerRuntimeFeatures([]string{"scan", "-i", "127.0.0.1", "--verify", "critical"})
		if err != nil {
			t.Fatalf("DirectScannerRuntimeFeatures() error = %v", err)
		}
		if !features.ProviderEnabled || features.ProviderOptional || !features.AIEnabled || !features.ScannerAI {
			t.Fatalf("features = %#v", features)
		}

		features, _, err = runner.DirectScannerRuntimeFeatures([]string{"scan", "-i", "127.0.0.1", "--sniper"})
		if err != nil {
			t.Fatalf("DirectScannerRuntimeFeatures() error = %v", err)
		}
		if !features.ProviderEnabled || features.ProviderOptional || !features.AIEnabled || !features.ScannerAI {
			t.Fatalf("sniper features = %#v", features)
		}

		features, args, err = runner.DirectScannerRuntimeFeatures([]string{"scan", "-i", "127.0.0.1", "--ai"})
		if err != nil {
			t.Fatalf("DirectScannerRuntimeFeatures() error = %v", err)
		}
		if features.ProviderEnabled || features.AIEnabled || features.ScannerAI {
			t.Fatalf("scan-local --ai should not enable AI features: %#v", features)
		}
		if !reflect.DeepEqual(args, []string{"scan", "-i", "127.0.0.1", "--ai"}) {
			t.Fatalf("args = %#v", args)
		}
	})
}

func TestAppConfigUsesCompiledDefaults(t *testing.T) {
	withDefaults(t, func() {
		cfg.DefaultCyberhubURL = "http://hub:8080"
		cfg.DefaultCyberhubKey = "HUBKEY"
		cfg.DefaultCyberhubMode = "override"
		cfg.DefaultVerifyTimeout = "77"
		cfg.DefaultTavilyKeys = "BUILTIN_TAVILY"
		cfg.DefaultIOAURL = "http://ioa:8765"
		cfg.DefaultIOANodeID = "node-1"
		cfg.DefaultIOANodeName = "worker-1"
		cfg.DefaultSpace = "case-1"

		opt := &cfg.Option{}
		cfg.ApplyDefaults(opt)
		appCfg := cfg.AppConfig(opt, cfg.RuntimeFeatures{
			ProviderEnabled:  true,
			ProviderOptional: true,
			AIEnabled:        true,
		}, telemetry.NopLogger())
		if appCfg.Scanner.CyberhubURL != cfg.DefaultCyberhubURL || appCfg.Scanner.CyberhubKey != cfg.DefaultCyberhubKey || appCfg.Scanner.CyberhubMode != cfg.DefaultCyberhubMode {
			t.Fatalf("scanner cyberhub config = %#v", appCfg.Scanner)
		}
		if !appCfg.Scanner.AIEnabled || appCfg.Scanner.AITimeout != 77 {
			t.Fatalf("scanner AI config = %#v", appCfg.Scanner)
		}
		if appCfg.Tools.TavilyKeys != cfg.DefaultTavilyKeys {
			t.Fatalf("tool search config = %#v", appCfg.Tools)
		}
		if !appCfg.Provider.Enabled || !appCfg.Provider.Optional {
			t.Fatalf("provider config = %#v", appCfg.Provider)
		}
		if opt.IOAURL != cfg.DefaultIOAURL || opt.IOANodeID != cfg.DefaultIOANodeID || opt.IOANodeName != cfg.DefaultIOANodeName || opt.Space != cfg.DefaultSpace {
			t.Fatal("compiled IOA defaults were not resolved")
		}
	})
}

func withDefaults(t *testing.T, fn func()) {
	t.Helper()
	saved := []struct {
		p *string
		v string
	}{
		{&cfg.DefaultProvider, cfg.DefaultProvider},
		{&cfg.DefaultBaseURL, cfg.DefaultBaseURL},
		{&cfg.DefaultAPIKey, cfg.DefaultAPIKey},
		{&cfg.DefaultModel, cfg.DefaultModel},
		{&cfg.DefaultScannerProxy, cfg.DefaultScannerProxy},
		{&cfg.DefaultCyberhubURL, cfg.DefaultCyberhubURL},
		{&cfg.DefaultCyberhubKey, cfg.DefaultCyberhubKey},
		{&cfg.DefaultCyberhubMode, cfg.DefaultCyberhubMode},
		{&cfg.DefaultVerify, cfg.DefaultVerify},
		{&cfg.DefaultVerifyTimeout, cfg.DefaultVerifyTimeout},
		{&cfg.DefaultTavilyKeys, cfg.DefaultTavilyKeys},
		{&cfg.DefaultIOAURL, cfg.DefaultIOAURL},
		{&cfg.DefaultIOANodeID, cfg.DefaultIOANodeID},
		{&cfg.DefaultIOANodeName, cfg.DefaultIOANodeName},
		{&cfg.DefaultSpace, cfg.DefaultSpace},
	}
	t.Cleanup(func() {
		for _, s := range saved {
			*s.p = s.v
		}
	})
	fn()
}

