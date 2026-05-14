package cmd

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/provider"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tool"
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
	if cfg.Provider != "deepseek" {
		t.Fatalf("provider = %q, want deepseek", cfg.Provider)
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
	if cfg.Provider != "deepseek" {
		t.Fatalf("provider = %q, want deepseek", cfg.Provider)
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
		"cyberhub",
		"search", "poc", "spring",
		"--cyberhub-key", "HUBKEY",
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

func TestParseCLIScannerRootArgsAfterPassthroughCommand(t *testing.T) {
	parsed, err := parseCLI([]string{
		"gogo",
		"-i", "127.0.0.1",
		"--cyberhub-url", "http://hub:8080",
		"--cyberhub-key", "HUBKEY",
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

func TestParseCLIPassthroughScannerExtractsAIIntentArgs(t *testing.T) {
	parsed, err := parseCLI([]string{
		"--api-key", "KEY",
		"--prompt", "review focus fingerprints",
		"--skill", "scan",
		"gogo",
		"-i", "127.0.0.1",
		"--ai",
		"--model", "deepseek-v4-pro",
		"--skill=aiscan",
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
	session := agent.New(llm, tool.NewToolRegistry())
	repl := newAgentConsole(context.Background(), &Option{}, &app.App{Skills: store}, session)

	if err := repl.executeArgs(context.Background(), []string{agentPromptCommandName, "hello"}); err != nil {
		t.Fatalf("executeArgs() error = %v", err)
	}
	if llm.requests != 1 {
		t.Fatalf("provider requests = %d, want 1", llm.requests)
	}
}

func TestParseCLIACPServeCommandUsesURL(t *testing.T) {
	parsed, err := parseCLI([]string{
		"acp",
		"serve",
		"--acp-url", "http://127.0.0.1:9999",
		"--acp-db", "./test.db",
		"--timeout", "10",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Mode != runModeACPServe {
		t.Fatalf("mode = %s, want %s", parsed.Mode, runModeACPServe)
	}
	opt := parsed.Option
	if opt.ACPURL != "http://127.0.0.1:9999" || opt.ACPDB != "./test.db" || opt.Timeout != 10 {
		t.Fatalf("option = %#v", opt)
	}
}

func TestDirectScannerRuntimeFeaturesForVerifyModes(t *testing.T) {
	withDefaults(t, func() {
		DefaultVerify = "auto"
		features, args, err := directScannerRuntimeFeatures([]string{"scan", "-i", "127.0.0.1"})
		if err != nil {
			t.Fatalf("directScannerRuntimeFeatures() error = %v", err)
		}
		if !features.ProviderEnabled || !features.ProviderOptional || !features.VerificationEnabled || features.VerifyMinPriority != "high" {
			t.Fatalf("features = %#v", features)
		}
		if !reflect.DeepEqual(args, []string{"scan", "-i", "127.0.0.1"}) {
			t.Fatalf("args = %#v", args)
		}

		features, args, err = directScannerRuntimeFeatures([]string{"scan", "-i", "127.0.0.1", "--verify=off"})
		if err != nil {
			t.Fatalf("directScannerRuntimeFeatures() error = %v", err)
		}
		if features.ProviderEnabled || features.VerificationEnabled {
			t.Fatalf("features = %#v", features)
		}
		if !reflect.DeepEqual(args, []string{"scan", "-i", "127.0.0.1", "--verify=off"}) {
			t.Fatalf("args = %#v", args)
		}

		features, _, err = directScannerRuntimeFeatures([]string{"scan", "-i", "127.0.0.1", "--verify", "critical"})
		if err != nil {
			t.Fatalf("directScannerRuntimeFeatures() error = %v", err)
		}
		if !features.ProviderEnabled || features.ProviderOptional || !features.VerificationEnabled || features.VerifyMinPriority != "critical" {
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
		DefaultACPURL = "http://acp:8765"
		DefaultACPNodeID = "node-1"
		DefaultACPNodeName = "worker-1"
		DefaultSpace = "case-1"

		opt := &Option{}
		applyDefaults(opt)
		cfg := appConfig(opt, runtimeFeatures{
			ProviderEnabled:     true,
			ProviderOptional:    true,
			VerificationEnabled: true,
			VerifyMinPriority:   "critical",
		}, telemetry.NopLogger())
		if cfg.Scanner.CyberhubURL != DefaultCyberhubURL || cfg.Scanner.CyberhubKey != DefaultCyberhubKey || cfg.Scanner.CyberhubMode != DefaultCyberhubMode {
			t.Fatalf("scanner cyberhub config = %#v", cfg.Scanner)
		}
		if !cfg.Scanner.VerificationEnabled || cfg.Scanner.VerifyMinPriority != "critical" || cfg.Scanner.VerifyTimeout != 77 {
			t.Fatalf("scanner verification config = %#v", cfg.Scanner)
		}
		if !cfg.Provider.Enabled || !cfg.Provider.Optional {
			t.Fatalf("provider config = %#v", cfg.Provider)
		}
		if opt.ACPURL != DefaultACPURL || opt.ACPNodeID != DefaultACPNodeID || opt.ACPNodeName != DefaultACPNodeName || opt.Space != DefaultSpace {
			t.Fatal("compiled ACP defaults were not resolved")
		}
	})
}

func withDefaults(t *testing.T, fn func()) {
	t.Helper()
	savedProvider := DefaultProvider
	savedBaseURL := DefaultBaseURL
	savedAPIKey := DefaultAPIKey
	savedModel := DefaultModel
	savedProxy := DefaultProxy
	savedCyberhubURL := DefaultCyberhubURL
	savedCyberhubKey := DefaultCyberhubKey
	savedCyberhubMode := DefaultCyberhubMode
	savedVerify := DefaultVerify
	savedVerifyTimeout := DefaultVerifyTimeout
	savedACPURL := DefaultACPURL
	savedACPNodeID := DefaultACPNodeID
	savedACPNodeName := DefaultACPNodeName
	savedSpace := DefaultSpace
	t.Cleanup(func() {
		DefaultProvider = savedProvider
		DefaultBaseURL = savedBaseURL
		DefaultAPIKey = savedAPIKey
		DefaultModel = savedModel
		DefaultProxy = savedProxy
		DefaultCyberhubURL = savedCyberhubURL
		DefaultCyberhubKey = savedCyberhubKey
		DefaultCyberhubMode = savedCyberhubMode
		DefaultVerify = savedVerify
		DefaultVerifyTimeout = savedVerifyTimeout
		DefaultACPURL = savedACPURL
		DefaultACPNodeID = savedACPNodeID
		DefaultACPNodeName = savedACPNodeName
		DefaultSpace = savedSpace
	})
	fn()
}
