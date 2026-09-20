package main

import (
	"bytes"
	"context"
	ioaclient "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/core/telemetry"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	cfg "github.com/chainreactors/cyber/pkg/config"
	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
	"github.com/chainreactors/cyber/pkg/profile"

	goflags "github.com/jessevdk/go-flags"
)

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func TestParseCLIScanExtractsLLMAndPassesScannerArgs(t *testing.T) {
	parsed, err := parseCLI([]string{
		"--cyberhub-url", "http://hub:8080",
		"--max-tokens", "16384",
		"scan",
		"-i", "127.0.0.1",
		"--verify=high",
		"--api-key", "KEY",
		"--model=deepseek-v4-pro",
		"--context-window=1000000",
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
	if opt.MaxTokens != 16384 || opt.ContextWindow != 1000000 {
		t.Fatalf("llm limits = max:%d context:%d", opt.MaxTokens, opt.ContextWindow)
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

func TestParseCLIScannerExtractsRootTimeout(t *testing.T) {
	for _, args := range [][]string{
		{"--timeout", "45", "gogo", "-i", "127.0.0.1", "-p", "80"},
		{"--timeout=45", "gogo", "-i", "127.0.0.1", "-p", "80"},
	} {
		t.Run(strings.Join(args[:1], "_"), func(t *testing.T) {
			parsed, err := parseCLI(args)
			if err != nil {
				t.Fatalf("parseCLI() error = %v", err)
			}
			if parsed.Option.Timeout != 45 {
				t.Fatalf("timeout = %d, want 45", parsed.Option.Timeout)
			}
			wantArgs := []string{"gogo", "-i", "127.0.0.1", "-p", "80"}
			if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
				t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
			}
		})
	}
}

func TestParseCLIScannerKeepsToolTimeoutAfterCommand(t *testing.T) {
	parsed, err := parseCLI([]string{"gogo", "-i", "127.0.0.1", "--timeout", "5"})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Option.Timeout != 3600 {
		t.Fatalf("overall timeout = %d, want default 3600", parsed.Option.Timeout)
	}
	wantArgs := []string{"gogo", "-i", "127.0.0.1", "--timeout", "5"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
}

func TestParseCLIScanKeepsNativeJSONFlag(t *testing.T) {
	parsed, err := parseCLI([]string{"scan", "-i", "127.0.0.1", "--json"})
	if err != nil {
		t.Fatalf("parseCLI: %v", err)
	}
	if parsed.Option.JSON {
		t.Fatal("scan-native --json was captured as the agent output flag")
	}
	if !slices.Contains(parsed.ScannerArgs, "--json") {
		t.Fatalf("scanner args = %#v, want native --json", parsed.ScannerArgs)
	}
}

func TestParseCLIIOAKeepsQueryJSONFlag(t *testing.T) {
	parsed, err := parseCLI([]string{"ioa", "spaces", "--json"})
	if err != nil {
		t.Fatalf("parseCLI: %v", err)
	}
	if parsed.Action == nil {
		t.Fatalf("IOA options = %#v, want query JSON", parsed.Action)
	}
	if parsed.Option.JSON {
		t.Fatal("IOA --json was captured as the agent output flag")
	}
}

func TestParseCLIAgentMachineOutput(t *testing.T) {
	parsed, err := parseCLI([]string{"agent", "-p", "hello", "--json", "--observe=files,http", "-o", "run.jsonl"})
	if err != nil {
		t.Fatalf("parseCLI: %v", err)
	}
	if parsed.Option.OutputFormat != "json" || parsed.Option.Observe != "files,http" || parsed.Option.OutputFile != "run.jsonl" {
		t.Fatalf("machine output options = %#v", parsed.Option.MiscOptions)
	}
	if _, err := parseCLI([]string{"agent", "-p", "hello", "--output-format", "yaml"}); err == nil {
		t.Fatal("unsupported agent output format was accepted")
	}
}

func TestParseCLIExtractsOutputForAgentAndScan(t *testing.T) {
	tests := []struct {
		args     []string
		wantArgs []string
	}{
		{args: []string{"agent", "-p", "hello", "-o", "agent.jsonl"}},
		{args: []string{"scan", "-i", "127.0.0.1", "-o", "scan.jsonl"}, wantArgs: []string{"scan", "-i", "127.0.0.1"}},
	}
	for _, test := range tests {
		t.Run(test.args[0], func(t *testing.T) {
			parsed, err := parseCLI(test.args)
			if err != nil {
				t.Fatalf("parseCLI: %v", err)
			}
			wantFile := test.args[len(test.args)-1]
			if parsed.Option.OutputFile != wantFile {
				t.Fatalf("output file = %q, want %q", parsed.Option.OutputFile, wantFile)
			}
			if test.wantArgs != nil && !reflect.DeepEqual(parsed.ScannerArgs, test.wantArgs) {
				t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, test.wantArgs)
			}
		})
	}
}

func TestParseCLIDirectScannerOwnsPostCommandOutputFlag(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "curl", args: []string{"curl", "-o", "body.txt", "http://127.0.0.1"}},
		{name: "gogo", args: []string{"gogo", "-i", "127.0.0.1", "-o", "jl"}},
		{name: "neutron", args: []string{"neutron", "-i", "http://127.0.0.1", "-o", "result.jsonl"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := parseCLI(test.args)
			if err != nil {
				t.Fatalf("parseCLI: %v", err)
			}
			if parsed.Option.OutputFile != "" {
				t.Fatalf("global output file captured scanner flag: %q", parsed.Option.OutputFile)
			}
			if !reflect.DeepEqual(parsed.ScannerArgs, test.args) {
				t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, test.args)
			}
		})
	}
}

func TestParseCLIDirectScannerAcceptsGlobalOutputBeforeCommand(t *testing.T) {
	parsed, err := parseCLI([]string{"-o", "events.jsonl", "gogo", "-i", "127.0.0.1", "-o", "jl"})
	if err != nil {
		t.Fatalf("parseCLI: %v", err)
	}
	if parsed.Option.OutputFile != "events.jsonl" {
		t.Fatalf("global output file = %q", parsed.Option.OutputFile)
	}
	wantArgs := []string{"gogo", "-i", "127.0.0.1", "-o", "jl"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
}

func TestParseCLIViewUsesUnifiedInputAndFileFlags(t *testing.T) {
	parsed, err := parseCLI([]string{"-F", "session.jsonl", "--view-format", "markdown", "-f", "session.md"})
	if err != nil {
		t.Fatalf("parseCLI: %v", err)
	}
	if parsed.Option.ViewFile != "session.jsonl" || parsed.Option.ViewFormat != "markdown" || parsed.Option.ViewOutput != "session.md" || parsed.Option.OutputFile != "" {
		t.Fatalf("view options = %#v", parsed.Option.MiscOptions)
	}
}

func TestParseCLIAllowsIndependentResumeAndOutput(t *testing.T) {
	for _, args := range [][]string{
		{"agent", "-r", "session.jsonl", "-o", "other.jsonl"},
		{"scan", "-i", "127.0.0.1", "-r", "session.jsonl", "-o", "other.jsonl"},
	} {
		parsed, err := parseCLI(args)
		if err != nil {
			t.Fatalf("parseCLI(%v) error = %v", args, err)
		}
		if parsed.Option.Resume != "session.jsonl" || parsed.Option.OutputFile != "other.jsonl" {
			t.Fatalf("parseCLI(%v) options = %#v", args, parsed.Option)
		}
	}
}

func TestParseCLIRootTimeoutAppliesToAgent(t *testing.T) {
	parsed, err := parseCLI([]string{"--timeout", "45", "agent", "-p", "test"})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Option.Timeout != 45 {
		t.Fatalf("timeout = %d, want 45", parsed.Option.Timeout)
	}

	parsed, err = parseCLI([]string{"agent", "--timeout", "30", "-p", "test"})
	if err != nil {
		t.Fatalf("parseCLI() subcommand timeout error = %v", err)
	}
	if parsed.Option.Timeout != 30 {
		t.Fatalf("subcommand timeout = %d, want 30", parsed.Option.Timeout)
	}
}

// Extension commands take their options from the registry rather than from
// AgentOptions, so the root --timeout is the only source of the overall
// deadline. A zero value expires the context before the command can run.
func TestParseCLIDefaultsOverallTimeoutForExtensionCommands(t *testing.T) {
	parsed, err := parseCLI([]string{"ioa", "spaces"})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Action == nil {
		t.Fatal("ioa spaces did not select an action")
	}
	if parsed.Option.Timeout != 3600 {
		t.Fatalf("timeout = %d, want default 3600", parsed.Option.Timeout)
	}

	parsed, err = parseCLI([]string{"--timeout", "45", "ioa", "spaces"})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Option.Timeout != 45 {
		t.Fatalf("timeout = %d, want 45", parsed.Option.Timeout)
	}
}

func TestDirectScannerModeSuppressesInitInfoByDefault(t *testing.T) {
	var logBuf bytes.Buffer
	logger := telemetry.NewLogger(telemetry.LogConfig{Output: &logBuf})
	err := runDirectScannerMode(context.Background(), newCyberProfileFromRequest, &cfg.Option{
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
	err := runDirectScannerMode(context.Background(), newCyberProfileFromRequest, &cfg.Option{
		MiscOptions: cfg.MiscOptions{Debug: true, NoColor: true},
	}, []string{"scan", "-i", "http://127.0.0.1:1", "--timeout", "1", "--no-color"}, logger)
	if err != nil {
		t.Fatalf("RunDirectScannerMode() error = %v", err)
	}
	logText := logBuf.String()
	if !strings.Contains(logText, "fingers") || !strings.Contains(logText, "scanner") {
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
	pcfg := apppkg.ProviderConfig(&opt)
	if pcfg.Provider != "openai" {
		t.Fatalf("provider = %q, want openai protocol", pcfg.Provider)
	}
	resolved, err := agent.ResolveProvider(&pcfg)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Provider != "openai" {
		t.Fatalf("resolved provider = %q, want openai (DeepSeek uses OpenAI-compatible protocol)", resolved.Provider)
	}
}

func TestAgentHelpRendersAgentOptionsWithoutRootCatalog(t *testing.T) {
	var cli cliOptions
	parser := newCLIParser(&cli, parserOptionsForArgs([]string{"agent", "-h"}))
	_, err := parser.ParseArgs([]string{"agent", "-h"})
	flagsErr, ok := err.(*goflags.Error)
	if !ok || flagsErr.Type != goflags.ErrHelp {
		t.Fatalf("ParseArgs() error = %v, want ErrHelp", err)
	}

	var buf bytes.Buffer
	writeHelp(parser, &buf)
	help := buf.String()
	searchableHelp := help + "\n" + strings.Join(strings.Fields(help), " ")
	for _, wants := range [][]string{
		{"agent [OPTIONS]"},
		{"Agent Options:"},
		{"-v", "--verbose"},
		{"thinking and tool previews"},
		{"full tool results"},
		{"--prompt", "/prompt"},
		{"--transport", "/transport"},
		{"--server-url", "/server-url"},
	} {
		if !containsAny(searchableHelp, wants...) {
			want := strings.Join(wants, " or ")
			t.Fatalf("agent help missing %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "Advanced scanners:") || strings.Contains(help, "Server management:") {
		t.Fatalf("agent help leaked the root command catalog:\n%s", help)
	}
}

func TestScannerHelpRegistryUsesGeneratedFlagHelp(t *testing.T) {
	for _, name := range []string{"scan", "gogo", "spray", "zombie", "neutron"} {
		t.Run(name, func(t *testing.T) {
			help, ok := scannerext.Usage(name)
			if !ok {
				t.Fatalf("StaticScannerUsage(%q) was not registered", name)
			}
			if !strings.Contains(help, "Usage:") || !strings.Contains(help, name+" [OPTIONS]") {
				t.Fatalf("%s help was not rendered by its go-flags parser:\n%s", name, help)
			}
			if !strings.Contains(help, "Help Options:") {
				t.Fatalf("%s help is missing go-flags help options:\n%s", name, help)
			}
			if strings.Count(help, "\n") < 10 {
				t.Fatalf("%s help looks like a static placeholder:\n%s", name, help)
			}
		})
	}
}

func TestParseCLIProtonUsesDirectScannerMode(t *testing.T) {
	help, ok := scannerext.Usage("proton")
	if !ok {
		t.Fatal("proton scanner help was not registered")
	}
	for _, want := range []string{"Usage: proton", "--template-list", "--severity"} {
		if !strings.Contains(help, want) {
			t.Fatalf("proton help missing %q:\n%s", want, help)
		}
	}

	parsed, err := parseCLI([]string{"proton", "-i", "config.yaml", "-j"})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Mode != cfg.RunModeScanner {
		t.Fatalf("mode = %s, want %s", parsed.Mode, cfg.RunModeScanner)
	}
	wantArgs := []string{"proton", "-i", "config.yaml", "-j"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
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
	pcfg := apppkg.ProviderConfig(&opt)
	if pcfg.Provider != "openai" {
		t.Fatalf("provider = %q, want openai protocol", pcfg.Provider)
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
		"--skill=cyber",
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
	if !reflect.DeepEqual(opt.Skills, []string{"scan", "cyber"}) {
		t.Fatalf("skills = %#v", opt.Skills)
	}
}

func TestScannerAIIntentInjectsCommandSkill(t *testing.T) {
	store, diagnostics := skills.LoadEmbeddedStore()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	intent, err := store.ApplySelected("focus on risky exposed services", nil)
	if err != nil {
		t.Fatalf("ApplySelected() error = %v", err)
	}
	if !strings.Contains(intent, "focus on risky exposed services") {
		t.Fatalf("intent missing user text:\n%s", intent)
	}
}

func TestParseCLIAgentIOAFlag(t *testing.T) {
	parsed, err := parseCLI([]string{
		"--debug",
		"agent",
		"--cyberhub-mode", "override",
		"-p", "scan localhost",
		"-s", "cyber",
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
	if !opt.Debug || opt.Prompt != "scan localhost" || readClientOptions(t, &opt).Space != "case-1" || opt.Heartbeat != 5 || opt.Model != "gpt-4o" || opt.CyberhubMode != "override" {
		t.Fatalf("option = %#v", opt)
	}
	if !reflect.DeepEqual(opt.Skills, []string{"cyber"}) {
		t.Fatalf("skills = %#v", opt.Skills)
	}
}

func TestParseCLIAgentServerURL(t *testing.T) {
	parsed, err := parseCLI([]string{
		"agent",
		"--server-url", "http://token@127.0.0.1:8080",
		"--ioa-url", "http://ioa-token@ioa.example:8765",
		"--space", "case-1",
		"--node-name", "worker-1",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Mode != cfg.RunModeAgent {
		t.Fatalf("mode = %s, want %s", parsed.Mode, cfg.RunModeAgent)
	}
	opt := parsed.Option
	if opt.ServerURL != "http://token@127.0.0.1:8080" || readClientOptions(t, &opt).URL != "http://ioa-token@ioa.example:8765" || readClientOptions(t, &opt).Space != "case-1" || opt.NodeName != "worker-1" {
		t.Fatalf("option = %#v", opt)
	}
}

func TestParseCLIRejectsRemovedServerURLAliases(t *testing.T) {
	for _, args := range [][]string{
		{"agent", "--web-url", "http://127.0.0.1:8080"},
		{"ioa", "serve", "--server-url", "http://127.0.0.1:9999"},
	} {
		if _, err := parseCLI(args); err == nil {
			t.Fatalf("parseCLI(%q) accepted a removed flag", args)
		}
	}
}

func TestParseCLIIOAServeCommandUsesURL(t *testing.T) {
	parsed, err := parseCLI([]string{
		"ioa",
		"serve",
		"--ioa-url", "http://127.0.0.1:9999",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Action == nil || !parsed.Action.Persistent {
		t.Fatalf("missing persistent action: %+v", parsed)
	}
	opt := parsed.Option
	if opt.Extensions["ioa.server"]["url"] != "http://127.0.0.1:9999" {
		t.Fatalf("option.IOAURL = %q, want %q", readClientOptions(t, &opt).URL, "http://127.0.0.1:9999")
	}
}

func TestResolveScannerModeForVerifyModes(t *testing.T) {
	withDefaults(t, func() {
		cfg.DefaultVerify = "off"
		mode, args, err := resolveScannerMode([]string{"scan", "-i", "127.0.0.1"}, cfg.DefaultVerify)
		if err != nil {
			t.Fatalf("ResolveScannerMode() error = %v", err)
		}
		if mode.Provider != profile.ProviderDisabled {
			t.Fatalf("mode = %#v", mode)
		}
		if !reflect.DeepEqual(args, []string{"scan", "-i", "127.0.0.1"}) {
			t.Fatalf("args = %#v", args)
		}

		mode, args, err = resolveScannerMode([]string{"scan", "-i", "127.0.0.1", "--verify=off"}, cfg.DefaultVerify)
		if err != nil {
			t.Fatalf("ResolveScannerMode() error = %v", err)
		}
		if mode.Provider != profile.ProviderDisabled {
			t.Fatalf("mode = %#v", mode)
		}
		if !reflect.DeepEqual(args, []string{"scan", "-i", "127.0.0.1", "--verify=off"}) {
			t.Fatalf("args = %#v", args)
		}

		mode, args, err = resolveScannerMode([]string{"scan", "-i", "127.0.0.1", "--deep"}, cfg.DefaultVerify)
		if err != nil {
			t.Fatalf("ResolveScannerMode() error = %v", err)
		}
		if mode.Provider != profile.ProviderRequired || !mode.Agent {
			t.Fatalf("deep mode = %#v", mode)
		}
		if !reflect.DeepEqual(args, []string{"scan", "-i", "127.0.0.1", "--deep"}) {
			t.Fatalf("args = %#v", args)
		}

		mode, _, err = resolveScannerMode([]string{"scan", "-i", "127.0.0.1", "--verify", "critical"}, cfg.DefaultVerify)
		if err != nil {
			t.Fatalf("ResolveScannerMode() error = %v", err)
		}
		if mode.Provider != profile.ProviderRequired || !mode.Agent {
			t.Fatalf("mode = %#v", mode)
		}

		mode, _, err = resolveScannerMode([]string{"scan", "-i", "127.0.0.1", "--sniper"}, cfg.DefaultVerify)
		if err != nil {
			t.Fatalf("ResolveScannerMode() error = %v", err)
		}
		if mode.Provider != profile.ProviderRequired || !mode.Agent {
			t.Fatalf("sniper mode = %#v", mode)
		}

		mode, args, err = resolveScannerMode([]string{"scan", "-i", "127.0.0.1", "--ai"}, cfg.DefaultVerify)
		if err != nil {
			t.Fatalf("ResolveScannerMode() error = %v", err)
		}
		if mode.Provider != profile.ProviderDisabled || mode.Agent {
			t.Fatalf("scan-local --ai should not enable AI features: %#v", mode)
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
		cfg.DefaultTavilyKeys = "BUILTIN_TAVILY"
		cfg.DefaultNodeID = "node-1"
		cfg.DefaultNodeName = "worker-1"

		opt := &cfg.Option{}
		cfg.ApplyDefaults(opt)
		if opt.NodeID != cfg.DefaultNodeID || opt.NodeName != cfg.DefaultNodeName {
			t.Fatal("compiled node defaults were not resolved")
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
		{&cfg.DefaultTavilyKeys, cfg.DefaultTavilyKeys},
		{&cfg.DefaultNodeID, cfg.DefaultNodeID},
		{&cfg.DefaultNodeName, cfg.DefaultNodeName},
	}
	t.Cleanup(func() {
		for _, s := range saved {
			*s.p = s.v
		}
	})
	fn()
}

func readClientOptions(t *testing.T, option *cfg.Option) ioaclient.Options {
	t.Helper()
	value, err := ioaclient.ReadOptions(option)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
