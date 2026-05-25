package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/skills"
)

type fakeConsoleProvider struct {
	requests int
}

func (p *fakeConsoleProvider) Name() string { return "fake" }

func (p *fakeConsoleProvider) ChatCompletion(_ context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	p.requests++
	return &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{
			Message: provider.NewTextMessage("assistant", "ok"),
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
		"--vision",
		"--vision-base-url", "https://openrouter.ai/api/v1",
		"--vision-api-key", "VISION_KEY",
		"--vision-model", "qwen/qwen3.6-flash",
		"--cyberhub-key=HUBKEY",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Mode != runModeScanner {
		t.Fatalf("mode = %s, want %s", parsed.Mode, runModeScanner)
	}
	wantArgs := []string{"scan", "-i", "127.0.0.1", "--verify=high"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
	opt := parsed.Option
	if opt.APIKey != "KEY" || opt.Model != "deepseek-v4-pro" || opt.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("llm options = %#v", opt.LLMOptions)
	}
	if !opt.Vision {
		t.Fatalf("vision not enabled")
	}
	if opt.VisionBaseURL != "https://openrouter.ai/api/v1" || opt.VisionAPIKey != "VISION_KEY" || opt.VisionModel != "qwen/qwen3.6-flash" {
		t.Fatalf("vision options = %#v", opt.VisionOptions)
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
	stdout, err := captureStdoutForTest(t, func() error {
		return runDirectScannerMode(context.Background(), &Option{
			MiscOptions: MiscOptions{NoColor: true},
		}, []string{"scan", "-i", "http://127.0.0.1:1", "--timeout", "1", "--no-color"}, logger)
	})
	if err != nil {
		t.Fatalf("runDirectScannerMode() error = %v", err)
	}
	combined := stdout + logBuf.String()
	for _, unwanted := range []string{"provider init", "engine=fingers", "commands=", "resources type="} {
		if strings.Contains(combined, unwanted) {
			t.Fatalf("normal scanner output leaked init log %q:\nstdout:\n%s\nlogs:\n%s", unwanted, stdout, logBuf.String())
		}
	}
	if !strings.Contains(stdout, "[summary] completed") {
		t.Fatalf("stdout missing scan summary: %q", stdout)
	}
}

func TestDirectScannerModeDebugShowsInitInfo(t *testing.T) {
	var logBuf bytes.Buffer
	logger := telemetry.NewLogger(telemetry.LogConfig{Debug: true, Output: &logBuf})
	stdout, err := captureStdoutForTest(t, func() error {
		return runDirectScannerMode(context.Background(), &Option{
			MiscOptions: MiscOptions{Debug: true, NoColor: true},
		}, []string{"scan", "-i", "http://127.0.0.1:1", "--timeout", "1", "--no-color"}, logger)
	})
	if err != nil {
		t.Fatalf("runDirectScannerMode() error = %v", err)
	}
	logText := logBuf.String()
	if !strings.Contains(logText, "engine=fingers status=ready") || !strings.Contains(logText, "commands=") {
		t.Fatalf("debug scanner logs missing init detail:\nstdout:\n%s\nlogs:\n%s", stdout, logText)
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
	if parsed.Mode != runModeAgent {
		t.Fatalf("mode = %s, want %s", parsed.Mode, runModeAgent)
	}
	opt := parsed.Option
	if opt.BaseURL != "https://api.deepseek.com" || opt.APIKey != "KEY" || opt.Model != "deepseek-v4-pro" {
		t.Fatalf("llm options = %#v", opt.LLMOptions)
	}
	cfg := providerConfig(&opt)
	if cfg.Provider != "" {
		t.Fatalf("provider should be unresolved before provider.Resolve, got %q", cfg.Provider)
	}
	resolved, err := provider.Resolve(&cfg)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Provider != "deepseek" {
		t.Fatalf("resolved provider = %q, want deepseek", resolved.Provider)
	}
}

func TestParseCLIScanExtractsLLMFlags(t *testing.T) {
	parsed, err := parseCLI([]string{
		"scan",
		"-i", "127.0.0.1",
		"--base-url", "https://api.deepseek.com",
		"--api-key", "KEY",
		"--model", "deepseek-v4-pro",
		"--ai",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	wantArgs := []string{"scan", "-i", "127.0.0.1"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
	opt := parsed.Option
	if !opt.AI || opt.APIKey != "KEY" || opt.Model != "deepseek-v4-pro" || opt.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("llm options = %#v", opt.LLMOptions)
	}
	cfg := providerConfig(&opt)
	if cfg.Provider != "" {
		t.Fatalf("provider should be unresolved before provider.Resolve, got %q", cfg.Provider)
	}
	resolved, err := provider.Resolve(&cfg)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Provider != "deepseek" {
		t.Fatalf("resolved provider = %q, want deepseek", resolved.Provider)
	}
}

func TestParseCLIScanAcceptsPromptShortFlagAfterCommand(t *testing.T) {
	parsed, err := parseCLI([]string{
		"scan",
		"-i", "127.0.0.1",
		"-p", "review focus fingerprints",
		"--ai",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if !parsed.Option.AI {
		t.Fatal("scan --ai should be enabled")
	}
	if parsed.Option.Prompt != "review focus fingerprints" {
		t.Fatalf("prompt = %q, want %q", parsed.Option.Prompt, "review focus fingerprints")
	}
	wantArgs := []string{"scan", "-i", "127.0.0.1"}
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

func TestParseCLICyberhubCommandPassthrough(t *testing.T) {
	parsed, err := parseCLI([]string{
		"--cyberhub-url", "http://hub:8080",
		"--cyberhub-key", "HUBKEY",
		"cyberhub",
		"search", "poc", "spring",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Mode != runModeScanner {
		t.Fatalf("mode = %s, want %s", parsed.Mode, runModeScanner)
	}
	wantArgs := []string{"cyberhub", "search", "poc", "spring"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
	if parsed.Option.CyberhubURL != "http://hub:8080" || parsed.Option.CyberhubKey != "HUBKEY" {
		t.Fatalf("scanner options = %#v", parsed.Option.ScannerOptions)
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
	if parsed.Mode != runModeScanner {
		t.Fatalf("mode = %s, want %s", parsed.Mode, runModeScanner)
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
	intent, err := resolveScannerAIIntent(&Option{AgentOptions: AgentOptions{Prompt: "focus on risky exposed services"}}, store, "gogo")
	if err != nil {
		t.Fatalf("resolveScannerAIIntent() error = %v", err)
	}
	for _, want := range []string{
		`<skill name="gogo" location="aiscan://skills/gogo/SKILL.md">`,
		"# Gogo",
		"focus on risky exposed services",
	} {
		if !strings.Contains(intent, want) {
			t.Fatalf("intent missing %q:\n%s", want, intent)
		}
	}
}

func TestParseCLIAgentLoopFlag(t *testing.T) {
	parsed, err := parseCLI([]string{
		"--debug",
		"--cyberhub-mode", "override",
		"agent",
		"--loop",
		"-p", "scan localhost",
		"-s", "aiscan",
		"--space", "case-1",
		"--heartbeat", "5",
		"--model", "gpt-4o",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Mode != runModeAgent {
		t.Fatalf("mode = %s, want %s", parsed.Mode, runModeAgent)
	}
	opt := parsed.Option
	if !opt.Debug || !opt.Loop || opt.Prompt != "scan localhost" || opt.Space != "case-1" || opt.Heartbeat != 5 || opt.Model != "gpt-4o" || opt.CyberhubMode != "override" {
		t.Fatalf("option = %#v", opt)
	}
	if !reflect.DeepEqual(opt.Skills, []string{"aiscan"}) {
		t.Fatalf("skills = %#v", opt.Skills)
	}
}

func TestParseCLILoopCommandRemoved(t *testing.T) {
	parsed, err := parseCLI([]string{"loop"})
	if err == nil && parsed.Mode != runModeNoCommand {
		t.Fatalf("mode = %s, want no command or parse error", parsed.Mode)
	}
}

func TestAgentConsoleArgsForLine(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantArgs []string
	}{
		{name: "empty", input: "  ", wantArgs: nil},
		{name: "prompt", input: " scan localhost ", wantArgs: []string{agentPromptCommandName, "scan localhost"}},
		{name: "quoted prompt is preserved", input: `explain "scan result"`, wantArgs: []string{agentPromptCommandName, `explain "scan result"`}},
		{name: "help", input: "/help", wantArgs: []string{"/help"}},
		{name: "reset", input: "/reset", wantArgs: []string{"/reset"}},
		{name: "continue", input: "/continue", wantArgs: []string{"/continue"}},
		{name: "exit", input: "/exit", wantArgs: []string{"/exit"}},
		{name: "quit", input: "/quit", wantArgs: []string{"/quit"}},
		{name: "skill slash command preserves prompt", input: `/scan explain "scan result"`, wantArgs: []string{"/scan", `explain "scan result"`}},
		{name: "unknown slash command", input: "/unknown", wantArgs: []string{"/unknown"}},
		{name: "legacy skill command", input: "/skill:scan check target", wantArgs: []string{agentPromptCommandName, "/skill:scan check target"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotArgs, err := agentConsoleArgsForLine(tt.input)
			if err != nil {
				t.Fatalf("agentConsoleArgsForLine() error = %v", err)
			}
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Fatalf("agentConsoleArgsForLine() = %#v, want %#v", gotArgs, tt.wantArgs)
			}
		})
	}
}

func TestAgentConsoleRegistersSkillsAsSlashCommands(t *testing.T) {
	store, diagnostics := skills.LoadEmbeddedStore()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	repl := &agentConsole{application: &app.App{Skills: store}}
	root := repl.rootCommand()

	for _, name := range []string{"aiscan", "scan", "gogo", "spray", "zombie", "neutron"} {
		cmd, _, err := root.Find([]string{"/" + name, "test"})
		if err != nil {
			t.Fatalf("find /%s error = %v", name, err)
		}
		if cmd == nil || cmd.Name() != "/"+name {
			t.Fatalf("find /%s = %#v", name, cmd)
		}
	}
}

func TestAgentConsolePromptCommandRunsAgent(t *testing.T) {
	store, diagnostics := skills.LoadEmbeddedStore()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	llm := &fakeConsoleProvider{}
	session := (agent.Config{Provider: llm, Tools: command.NewRegistry()}).NewAgent()
	repl := newAgentConsole(context.Background(), &Option{}, &app.App{Skills: store}, session)

	if err := repl.executeArgs(context.Background(), []string{agentPromptCommandName, "hello"}); err != nil {
		t.Fatalf("executeArgs() error = %v", err)
	}
	if llm.requests != 1 {
		t.Fatalf("provider requests = %d, want 1", llm.requests)
	}
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
	if parsed.Mode != runModeIOAServe {
		t.Fatalf("mode = %s, want %s", parsed.Mode, runModeIOAServe)
	}
	opt := parsed.Option
	if opt.IOAURL != "http://127.0.0.1:9999" || opt.Timeout != 10 {
		t.Fatalf("option = %#v", opt)
	}
}

func TestDirectScannerRuntimeFeaturesForVerifyModes(t *testing.T) {
	withDefaults(t, func() {
		DefaultVerify = "off"
		features, args, err := directScannerRuntimeFeatures([]string{"scan", "-i", "127.0.0.1"})
		if err != nil {
			t.Fatalf("directScannerRuntimeFeatures() error = %v", err)
		}
		if features.ProviderEnabled || features.AIEnabled {
			t.Fatalf("features = %#v", features)
		}
		if !reflect.DeepEqual(args, []string{"scan", "-i", "127.0.0.1"}) {
			t.Fatalf("args = %#v", args)
		}

		features, args, err = directScannerRuntimeFeatures([]string{"scan", "-i", "127.0.0.1", "--verify=off"})
		if err != nil {
			t.Fatalf("directScannerRuntimeFeatures() error = %v", err)
		}
		if features.ProviderEnabled || features.AIEnabled {
			t.Fatalf("features = %#v", features)
		}
		if features.Warning == "" {
			t.Fatalf("deprecated verify warning missing: %#v", features)
		}
		if !reflect.DeepEqual(args, []string{"scan", "-i", "127.0.0.1", "--verify=off"}) {
			t.Fatalf("args = %#v", args)
		}

		features, _, err = directScannerRuntimeFeatures([]string{"scan", "-i", "127.0.0.1", "--verify", "critical"})
		if err != nil {
			t.Fatalf("directScannerRuntimeFeatures() error = %v", err)
		}
		if !features.ProviderEnabled || features.ProviderOptional || !features.AIEnabled || !features.ScannerAI {
			t.Fatalf("features = %#v", features)
		}
	})
}

func TestAppConfigUsesCompiledDefaults(t *testing.T) {
	withDefaults(t, func() {
		DefaultCyberhubURL = "http://hub:8080"
		DefaultCyberhubKey = "HUBKEY"
		DefaultCyberhubMode = "override"
		DefaultVerifyTimeout = "77"
		DefaultIOAURL = "http://ioa:8765"
		DefaultIOANodeID = "node-1"
		DefaultIOANodeName = "worker-1"
		DefaultSpace = "case-1"

		opt := &Option{}
		applyDefaults(opt)
		cfg := appConfig(opt, runtimeFeatures{
			ProviderEnabled:  true,
			ProviderOptional: true,
			AIEnabled:        true,
		}, telemetry.NopLogger())
		if cfg.Scanner.CyberhubURL != DefaultCyberhubURL || cfg.Scanner.CyberhubKey != DefaultCyberhubKey || cfg.Scanner.CyberhubMode != DefaultCyberhubMode {
			t.Fatalf("scanner cyberhub config = %#v", cfg.Scanner)
		}
		if !cfg.Scanner.AIEnabled || cfg.Scanner.AITimeout != 77 {
			t.Fatalf("scanner AI config = %#v", cfg.Scanner)
		}
		if !cfg.Provider.Enabled || !cfg.Provider.Optional {
			t.Fatalf("provider config = %#v", cfg.Provider)
		}
		if opt.IOAURL != DefaultIOAURL || opt.IOANodeID != DefaultIOANodeID || opt.IOANodeName != DefaultIOANodeName || opt.Space != DefaultSpace {
			t.Fatal("compiled IOA defaults were not resolved")
		}
	})
}

func withDefaults(t *testing.T, fn func()) {
	t.Helper()
	savedProvider := DefaultProvider
	savedBaseURL := DefaultBaseURL
	savedAPIKey := DefaultAPIKey
	savedModel := DefaultModel
	savedScannerProxy := DefaultScannerProxy
	savedCyberhubURL := DefaultCyberhubURL
	savedCyberhubKey := DefaultCyberhubKey
	savedCyberhubMode := DefaultCyberhubMode
	savedVerify := DefaultVerify
	savedVerifyTimeout := DefaultVerifyTimeout
	savedIOAURL := DefaultIOAURL
	savedIOANodeID := DefaultIOANodeID
	savedIOANodeName := DefaultIOANodeName
	savedSpace := DefaultSpace
	t.Cleanup(func() {
		DefaultProvider = savedProvider
		DefaultBaseURL = savedBaseURL
		DefaultAPIKey = savedAPIKey
		DefaultModel = savedModel
		DefaultScannerProxy = savedScannerProxy
		DefaultCyberhubURL = savedCyberhubURL
		DefaultCyberhubKey = savedCyberhubKey
		DefaultCyberhubMode = savedCyberhubMode
		DefaultVerify = savedVerify
		DefaultVerifyTimeout = savedVerifyTimeout
		DefaultIOAURL = savedIOAURL
		DefaultIOANodeID = savedIOANodeID
		DefaultIOANodeName = savedIOANodeName
		DefaultSpace = savedSpace
	})
	fn()
}

func captureStdoutForTest(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	os.Stdout = writer
	defer func() { os.Stdout = oldStdout }()

	readCh := make(chan []byte, 1)
	readErrCh := make(chan error, 1)
	go func() {
		data, readErr := io.ReadAll(reader)
		readCh <- data
		readErrCh <- readErr
	}()

	runErr := fn()
	closeErr := writer.Close()
	data := <-readCh
	readErr := <-readErrCh
	_ = reader.Close()
	if readErr != nil {
		t.Fatalf("read captured stdout: %v", readErr)
	}
	if runErr == nil {
		runErr = closeErr
	}
	return string(data), runErr
}
