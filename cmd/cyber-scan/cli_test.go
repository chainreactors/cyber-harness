package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	provider "github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/telemetry"
	ioaclient "github.com/chainreactors/cyber/exts/ioa/client"
	scannerext "github.com/chainreactors/cyber/exts/scanner"
	searchext "github.com/chainreactors/cyber/exts/search"
	taskcli "github.com/chainreactors/cyber/pkg/cli/task"
	cfg "github.com/chainreactors/cyber/pkg/config"
	goflags "github.com/jessevdk/go-flags"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"
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
	}, io.Discard)
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
	hub, err := scannerext.ReadCyberhub(&opt)
	if err != nil {
		t.Fatal(err)
	}
	if hub.URL != "http://hub:8080" || hub.Key != "HUBKEY" {
		t.Fatalf("scanner options = %#v", hub)
	}
}

func TestParseCLIScannerDebugEnablesGlobalDebugAndPreservesArg(t *testing.T) {
	parsed, err := parseCLI([]string{"scan", "-i", "127.0.0.1", "--debug"}, io.Discard)
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
			parsed, err := parseCLI(args, io.Discard)
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
	parsed, err := parseCLI([]string{"gogo", "-i", "127.0.0.1", "--timeout", "5"}, io.Discard)
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
	parsed, err := parseCLI([]string{"scan", "-i", "127.0.0.1", "--json"}, io.Discard)
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
	parsed, err := parseCLI([]string{"ioa", "spaces", "--json"}, io.Discard)
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
	parsed, err := parseCLI([]string{"agent", "-p", "hello", "-o", "json", "--observe=files,http", "-f", "run.json"}, io.Discard)
	if err != nil {
		t.Fatalf("parseCLI: %v", err)
	}
	if parsed.Option.OutputFormat != "json" || parsed.Option.Observe != "files,http" || parsed.Option.OutputFile != "run.json" {
		t.Fatalf("machine output options = %#v", parsed.Option.MiscOptions)
	}
	if _, err := parseCLI([]string{"agent", "-p", "hello", "--output-format", "yaml"}, io.Discard); err == nil {
		t.Fatal("unsupported agent output format was accepted")
	}
}

func TestParseCLIExtractsOutputForAgentAndScan(t *testing.T) {
	tests := []struct {
		args     []string
		wantArgs []string
	}{
		{args: []string{"agent", "-p", "hello", "-o", "jsonl", "-f", "agent.jsonl"}},
		{args: []string{"scan", "-i", "127.0.0.1", "-o", "jsonl", "-f", "scan.jsonl"}, wantArgs: []string{"scan", "-i", "127.0.0.1"}},
	}
	for _, test := range tests {
		t.Run(test.args[0], func(t *testing.T) {
			parsed, err := parseCLI(test.args, io.Discard)
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
			parsed, err := parseCLI(test.args, io.Discard)
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
	parsed, err := parseCLI([]string{"-o", "jsonl", "-f", "events.jsonl", "gogo", "-i", "127.0.0.1", "-o", "jl"}, io.Discard)
	if err != nil {
		t.Fatalf("parseCLI: %v", err)
	}
	if parsed.Option.OutputFormat != "jsonl" || parsed.Option.OutputFile != "events.jsonl" {
		t.Fatalf("global output options = %+v", parsed.Option.MiscOptions)
	}
	wantArgs := []string{"gogo", "-i", "127.0.0.1", "-o", "jl"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
}

func TestParseCLIScannerRetainsRootConfigLocations(t *testing.T) {
	for _, scanner := range []string{"scan", "gogo"} {
		t.Run(scanner, func(t *testing.T) {
			parsed, err := parseCLI([]string{
				"--config", "custom.yaml", "--data-dir", "custom-data",
				scanner, "-i", "127.0.0.1",
			}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Option.ConfigFile != "custom.yaml" || parsed.Option.DataDir != "custom-data" {
				t.Fatalf("root config locations were lost: %+v", parsed.Option.MiscOptions)
			}
			if !parsed.Option.Explicit["config"] || !parsed.Option.Explicit["data-dir"] {
				t.Fatalf("root config presence was lost: %v", parsed.Option.Explicit)
			}
		})
	}
}

func TestParseCLIViewUsesUnifiedInputAndFileFlags(t *testing.T) {
	parsed, err := parseCLI([]string{"-F", "session.jsonl", "-o", "markdown", "-f", "session.md"}, io.Discard)
	if err != nil {
		t.Fatalf("parseCLI: %v", err)
	}
	if parsed.Option.ViewFile != "session.jsonl" || parsed.Option.OutputFormat != "markdown" || parsed.Option.OutputFile != "session.md" {
		t.Fatalf("view options = %#v", parsed.Option.MiscOptions)
	}
}

func TestParseCLIReplayOutputSelectsFormat(t *testing.T) {
	for _, test := range []struct {
		args   []string
		format string
	}{
		{[]string{"-F", "session.jsonl", "-o", "csv", "-f", "results.csv"}, "csv"},
		{[]string{"-o", "csv", "-F", "session.jsonl", "-f", "results.csv"}, "csv"},
		{[]string{"--view=session.jsonl", "--output-format=CSV", "-f", "results.csv"}, "csv"},
		{[]string{"-F", "session.jsonl", "-o", "markdown", "-f", "results.csv"}, "markdown"},
		{[]string{"-F", "session.jsonl", "-o", "md", "-f", "results.csv"}, "markdown"},
		{[]string{"-F", "session.jsonl", "-o", "terminal", "-f", "results.csv"}, "text"},
	} {
		parsed, err := parseCLI(test.args, io.Discard)
		if err != nil {
			t.Fatalf("parseCLI(%q): %v", test.args, err)
		}
		if parsed.Option.OutputFormat != test.format || parsed.Option.OutputFile != "results.csv" {
			t.Fatalf("parseCLI(%q): replay options=%+v", test.args, parsed.Option.MiscOptions)
		}
	}
	parsed, err := parseCLI([]string{"-F", "session.jsonl", "-o", "csv"}, io.Discard)
	if err != nil || parsed.Option.OutputFormat != "csv" || parsed.Option.OutputFile != "" {
		t.Fatalf("stdout replay: parsed=%+v err=%v", parsed.Option.MiscOptions, err)
	}
	if _, err := parseCLI([]string{"-F", "session.jsonl", "--view-format", "csv"}, io.Discard); err == nil {
		t.Fatal("removed replay-specific format flag was accepted")
	}
}

func TestParseCLIUsesTheSameOutputFlagsAcrossModes(t *testing.T) {
	for _, mode := range [][]string{{"agent", "-p", "task"}, {"scan", "-i", "127.0.0.1"}, {"-F", "events.jsonl"}} {
		for _, test := range []struct{ input, want string }{
			{"text", "text"}, {"json", "json"}, {"jsonl", "jsonl"}, {"stream-json", "jsonl"},
			{"markdown", "markdown"}, {"md", "markdown"}, {"CSV", "csv"},
		} {
			args := append(append([]string(nil), mode...), "-o", test.input, "-f", "result.out")
			parsed, err := parseCLI(args, io.Discard)
			if err != nil || parsed.Option.OutputFormat != test.want || parsed.Option.OutputFile != "result.out" {
				t.Fatalf("%v: options=%+v error=%v", args, parsed.Option.MiscOptions, err)
			}
		}
		if _, err := parseCLI(append(append([]string(nil), mode...), "-o", "events.jsonl"), io.Discard); err == nil {
			t.Fatalf("%v interpreted -o as a filename", mode)
		}
	}
	if _, err := parseCLI([]string{"agent", "-p", "task", "-o", "csv", "--json"}, io.Discard); err == nil {
		t.Fatal("conflicting format aliases were accepted")
	}
}

func TestCLIReplaysScannerArtifactsAsCSVWithoutRuntimeConfiguration(t *testing.T) {
	dir := t.TempDir()
	input, output := filepath.Join(dir, "scan.jsonl"), filepath.Join(dir, "scan.csv")
	extension, err := anypb.New(&toolpb.Artifact{Tool: "gogo", Kind: "service", Target: "127.0.0.1:80", ResultId: "result",
		Data: []byte(`{"ip":"127.0.0.1","port":"80"}`)})
	if err != nil {
		t.Fatal(err)
	}
	event, err := protojson.Marshal(&aop.Event{Id: "artifact", SessionId: "scan", Payload: &aop.Event_Extension{Extension: extension}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, append(event, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := runCLI(t.Context(), []string{"-F", input, "-o", "csv", "-f", output}, strings.NewReader(""), &stdout, &stderr, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil || len(records) != 2 || records[1][0] != "127.0.0.1:80" || records[1][1] != "gogo" {
		t.Fatalf("CSV replay: records=%v err=%v", records, err)
	}
	if err := runCLI(t.Context(), []string{"-F", input, "-o", "cvs", "-f", output}, strings.NewReader(""), &stdout, &stderr, nil); err == nil || !strings.Contains(err.Error(), "unsupported output format") {
		t.Fatalf("invalid replay format error=%v", err)
	}
	if preserved, _ := os.ReadFile(output); !bytes.Equal(preserved, data) {
		t.Fatal("invalid format changed the previous export")
	}
}

func TestParseCLIAllowsIndependentResumeAndOutput(t *testing.T) {
	for _, args := range [][]string{
		{"agent", "-r", "session.jsonl", "-o", "jsonl", "-f", "other.jsonl"},
		{"scan", "-i", "127.0.0.1", "-r", "session.jsonl", "-o", "jsonl", "-f", "other.jsonl"},
	} {
		parsed, err := parseCLI(args, io.Discard)
		if err != nil {
			t.Fatalf("parseCLI(%v) error = %v", args, err)
		}
		if parsed.Option.Resume != "session.jsonl" || parsed.Option.OutputFile != "other.jsonl" {
			t.Fatalf("parseCLI(%v) options = %#v", args, parsed.Option)
		}
	}
}

func TestParseCLIRootTimeoutAppliesToAgent(t *testing.T) {
	parsed, err := parseCLI([]string{"--timeout", "45", "agent", "-p", "test"}, io.Discard)
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Option.Timeout != 45 {
		t.Fatalf("timeout = %d, want 45", parsed.Option.Timeout)
	}

	parsed, err = parseCLI([]string{"agent", "--timeout", "30", "-p", "test"}, io.Discard)
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
	parsed, err := parseCLI([]string{"ioa", "spaces"}, io.Discard)
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Action == nil {
		t.Fatal("ioa spaces did not select an action")
	}
	if parsed.Option.Timeout != 3600 {
		t.Fatalf("timeout = %d, want default 3600", parsed.Option.Timeout)
	}

	parsed, err = parseCLI([]string{"--timeout", "45", "ioa", "spaces"}, io.Discard)
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
	err := runDirectScannerMode(context.Background(), newCyberScanProfile, &cfg.Option{
		MiscOptions: cfg.MiscOptions{NoColor: true},
	}, []string{"scan", "-i", "http://127.0.0.1:1", "--timeout", "1", "--no-color"}, logger, taskcli.NewOutput(&cfg.Option{}, nil, nil))
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
	err := runDirectScannerMode(context.Background(), newCyberScanProfile, &cfg.Option{
		MiscOptions: cfg.MiscOptions{Debug: true, NoColor: true},
	}, []string{"scan", "-i", "http://127.0.0.1:1", "--timeout", "1", "--no-color"}, logger, taskcli.NewOutput(&cfg.Option{}, nil, nil))
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
	}, io.Discard)
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
	if pcfg.Provider != "openai" {
		t.Fatalf("provider = %q, want openai protocol", pcfg.Provider)
	}
	resolved, err := provider.Resolve(&pcfg)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Provider != "openai" {
		t.Fatalf("resolved provider = %q, want openai (DeepSeek uses OpenAI-compatible protocol)", resolved.Provider)
	}
}

func TestAgentHelpRendersAgentOptionsWithoutRootCatalog(t *testing.T) {
	var cli cliOptions
	parser := newCLIParser(&cli, goflags.Default&^goflags.PrintErrors)
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
			help, ok := productScannerUsage(name)
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
	help, ok := productScannerUsage("proton")
	if !ok {
		t.Fatal("proton scanner help was not registered")
	}
	for _, want := range []string{"Usage: proton", "--template-list", "--severity"} {
		if !strings.Contains(help, want) {
			t.Fatalf("proton help missing %q:\n%s", want, help)
		}
	}

	parsed, err := parseCLI([]string{"proton", "-i", "config.yaml", "-j"}, io.Discard)
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
	}, io.Discard)
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
	if pcfg.Provider != "openai" {
		t.Fatalf("provider = %q, want openai protocol", pcfg.Provider)
	}
	resolved, err := provider.Resolve(&pcfg)
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
	}, io.Discard)
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
	}, io.Discard)
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
	}, io.Discard)
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

func TestParseCLIKeepsScannerNamesInsideOtherCommands(t *testing.T) {
	for _, args := range [][]string{
		{"agent", "gogo"},
		{"agent", "--", "scan"},
		{"ioa", "spaces", "gogo"},
		{"--", "scan", "-i", "127.0.0.1"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			parsed, _ := parseCLI(args, io.Discard)
			if parsed.Mode == cfg.RunModeScanner || len(parsed.ScannerArgs) != 0 {
				t.Fatalf("scanner name inside another command became a scanner invocation: mode=%q args=%v", parsed.Mode, parsed.ScannerArgs)
			}
		})
	}
}

func TestParseCLIScannerAfterVerbosityFlags(t *testing.T) {
	for _, test := range []struct {
		flag, scanner string
		verbosity     int
	}{
		{"-v", "gogo", 1},
		{"--verbose", "scan", 1},
		{"-vv", "gogo", 2},
	} {
		t.Run(test.flag, func(t *testing.T) {
			parsed, err := parseCLI([]string{test.flag, test.scanner, "-i", "127.0.0.1"}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Mode != cfg.RunModeScanner || len(parsed.Option.Verbose) != test.verbosity {
				t.Fatalf("verbosity flag consumed the scanner command: mode=%q verbose=%v", parsed.Mode, parsed.Option.Verbose)
			}
		})
	}
}

func TestScannerArgumentsAfterTerminatorRemainLiteral(t *testing.T) {
	for _, scanner := range []string{"scan", "gogo"} {
		t.Run(scanner, func(t *testing.T) {
			args := []string{scanner, "-i", "127.0.0.1", "--", "--model", "literal", "--file", "literal", "--debug"}
			parsed, err := parseCLI(args, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(parsed.ScannerArgs, args) || parsed.Option.Model != "" || parsed.Option.OutputFile != "" || parsed.Option.Debug {
				t.Fatalf("literal scanner arguments changed host settings: args=%q model=%q file=%q debug=%v", parsed.ScannerArgs, parsed.Option.Model, parsed.Option.OutputFile, parsed.Option.Debug)
			}
		})
	}
	for _, verify := range []string{"", "off", "on"} {
		t.Run("verify="+verify, func(t *testing.T) {
			args := []string{"scan", "-i", "127.0.0.1", "--", "--verify=invalid", "--sniper", "--deep", "--help", "--json", "--report"}
			original := slices.Clone(args)
			mode, resolved, err := resolveScannerMode(args, verify)
			if err != nil {
				t.Fatal(err)
			}
			if mode.Agent != (verify == "on") {
				t.Fatalf("literal flags changed scanner mode: %+v", mode)
			}
			want := slices.Clone(original)
			if verify != "" {
				want = slices.Insert(want, 3, "--verify="+verify)
			}
			if !slices.Equal(resolved, want) || !slices.Equal(args, original) {
				t.Fatalf("scanner defaults crossed the terminator or changed the input: got=%q want=%q input=%q", resolved, want, args)
			}
			if cfg.IsScannerHelpRequest(resolved) || isDirectScannerJSONOutput(resolved) || !shouldStreamScannerOutput(resolved) {
				t.Fatalf("literal flags changed help or output behavior: %q", resolved)
			}
		})
	}
}

func TestParseCLICyberhubModeRootAndPassthrough(t *testing.T) {
	parsed, err := parseCLI([]string{
		"--cyberhub-mode", "override",
		"spray",
		"-u", "http://127.0.0.1:5000",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	hub, err := scannerext.ReadCyberhub(&parsed.Option)
	if err != nil {
		t.Fatal(err)
	}
	if hub.Mode != "override" {
		t.Fatalf("cyberhub mode = %q, want override", hub.Mode)
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
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	wantArgs := []string{"gogo", "-i", "127.0.0.1", "--cyberhub-url", "http://hub:8080", "--cyberhub-key", "HUBKEY"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
	if _, ok := parsed.Option.Extensions[scannerext.CyberhubConfigKey]; ok {
		t.Fatalf("scanner options leaked from post-command args: %#v", parsed.Option.Extensions)
	}
}

func TestParseCLIScannerRootArgsBeforeCommandStillApply(t *testing.T) {
	parsed, err := parseCLI([]string{
		"--cyberhub-url", "http://hub:8080",
		"--cyberhub-key", "HUBKEY",
		"gogo",
		"-i", "127.0.0.1",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	wantArgs := []string{"gogo", "-i", "127.0.0.1"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
	hub, err := scannerext.ReadCyberhub(&parsed.Option)
	if err != nil {
		t.Fatal(err)
	}
	if hub.URL != "http://hub:8080" || hub.Key != "HUBKEY" {
		t.Fatalf("scanner options = %#v", hub)
	}
}

func TestParseCLIGogoKeepsPortShortFlagAfterCommand(t *testing.T) {
	parsed, err := parseCLI([]string{
		"gogo",
		"-i", "127.0.0.1",
		"-p", "top100",
	}, io.Discard)
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
	}, io.Discard)
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
	}, io.Discard)
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
	}, io.Discard)
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
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Mode != cfg.RunModeAgent {
		t.Fatalf("mode = %s, want %s", parsed.Mode, cfg.RunModeAgent)
	}
	opt := parsed.Option
	hub, err := scannerext.ReadCyberhub(&opt)
	if err != nil {
		t.Fatal(err)
	}
	if !opt.Debug || opt.Prompt != "scan localhost" || readClientOptions(t, &opt).Space != "case-1" || opt.Heartbeat != 5 || opt.Model != "gpt-4o" || hub.Mode != "override" {
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
	}, io.Discard)
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
		if _, err := parseCLI(args, io.Discard); err == nil {
			t.Fatalf("parseCLI(%q) accepted a removed flag", args)
		}
	}
}

func TestParseCLIIOAServeCommandUsesURL(t *testing.T) {
	parsed, err := parseCLI([]string{
		"ioa",
		"serve",
		"--ioa-url", "http://127.0.0.1:9999",
	}, io.Discard)
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
	mode, args, err := resolveScannerMode([]string{"scan", "-i", "127.0.0.1"}, "")
	if err != nil {
		t.Fatalf("ResolveScannerMode() error = %v", err)
	}
	if mode.Provider != provider.StartupOptional {
		t.Fatalf("mode = %#v", mode)
	}
	if !reflect.DeepEqual(args, []string{"scan", "-i", "127.0.0.1"}) {
		t.Fatalf("args = %#v", args)
	}

	mode, args, err = resolveScannerMode([]string{"scan", "-i", "127.0.0.1", "--verify=off"}, "")
	if err != nil {
		t.Fatalf("ResolveScannerMode() error = %v", err)
	}
	if mode.Provider != provider.StartupOptional {
		t.Fatalf("mode = %#v", mode)
	}
	if !reflect.DeepEqual(args, []string{"scan", "-i", "127.0.0.1", "--verify=off"}) {
		t.Fatalf("args = %#v", args)
	}

	if _, _, err = resolveScannerMode([]string{"scan", "-i", "127.0.0.1", "--deep"}, ""); err == nil {
		t.Fatal("removed --deep flag was accepted")
	}

	mode, args, err = resolveScannerMode([]string{"scan", "-i", "127.0.0.1", "--verify=on"}, "")
	if err != nil {
		t.Fatalf("ResolveScannerMode() error = %v", err)
	}
	if mode.Provider != provider.StartupRequired || !mode.Agent {
		t.Fatalf("mode = %#v", mode)
	}
	if !reflect.DeepEqual(args, []string{"scan", "-i", "127.0.0.1", "--verify=on"}) {
		t.Fatalf("args = %#v", args)
	}

	mode, _, err = resolveScannerMode([]string{"scan", "-i", "127.0.0.1", "--sniper"}, "")
	if err != nil {
		t.Fatalf("ResolveScannerMode() error = %v", err)
	}
	if mode.Provider != provider.StartupRequired || !mode.Agent {
		t.Fatalf("sniper mode = %#v", mode)
	}

	mode, args, err = resolveScannerMode([]string{"scan", "-i", "127.0.0.1", "--ai"}, "")
	if err != nil {
		t.Fatalf("ResolveScannerMode() error = %v", err)
	}
	if mode.Provider != provider.StartupOptional || mode.Agent {
		t.Fatalf("scan-local --ai should not enable AI features: %#v", mode)
	}
	if !reflect.DeepEqual(args, []string{"scan", "-i", "127.0.0.1", "--ai"}) {
		t.Fatalf("args = %#v", args)
	}
}

func TestAppConfigUsesCompiledDefaults(t *testing.T) {
	t.Cleanup(scannerext.SetDefaults(scannerext.CyberhubOptions{URL: "http://hub:8080", Key: "HUBKEY", Mode: "override", Proxy: scannerext.DefaultScannerProxy}))
	t.Cleanup(searchext.SetDefaultKeys("BUILTIN_TAVILY"))
	t.Cleanup(cfg.SetNodeDefaults("node-1", "worker-1"))

	opt := &cfg.Option{}
	cfg.ApplyDefaults(opt)
	if opt.NodeID != cfg.DefaultNodeID || opt.NodeName != cfg.DefaultNodeName {
		t.Fatal("compiled node defaults were not resolved")
	}

	resolved, err := defaultSections().ResolveValues(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	hub, err := cfg.Get[*scannerext.CyberhubOptions](resolved, scannerext.CyberhubConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	if hub.URL != "http://hub:8080" || hub.Key != "HUBKEY" || hub.Mode != "override" {
		t.Fatalf("compiled cyberhub defaults were not resolved: %#v", hub)
	}
	search, err := cfg.Get[*searchext.Options](resolved, searchext.ConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	if search.TavilyKeys != "BUILTIN_TAVILY" {
		t.Fatalf("compiled tavily default was not resolved: %#v", search)
	}
}

func readClientOptions(t *testing.T, option *cfg.Option) ioaclient.Options {
	t.Helper()
	value, err := ioaclient.ReadOptions(option)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
