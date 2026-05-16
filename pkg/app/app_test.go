package app

import (
	"context"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/provider"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/chainreactors/sdk/gogo"
	"github.com/chainreactors/sdk/spray"

	// side-effect imports: register vision, webfetch, websearch factories
	_ "github.com/chainreactors/aiscan/pkg/tools/vision"
	_ "github.com/chainreactors/aiscan/pkg/tools/webfetch"
	_ "github.com/chainreactors/aiscan/pkg/tools/websearch"
)

func TestInitCommandRegistryRegistersVisionOnlyWhenConfigured(t *testing.T) {
	logger := telemetry.NopLogger()
	visionCfg := &provider.ProviderConfig{
		BaseURL: "http://example.test/v1",
		APIKey:  "test-key",
		Model:   "gpt-4o",
	}

	// Without vision config: vision should not be registered.
	reg := initCommandRegistry(nil, ScannerConfig{}, ToolConfig{}, nil, "", nil, nil, logger)
	if reg.Has("vision") {
		t.Fatal("vision command should not be registered without vision config")
	}

	// With vision config: vision should be registered.
	reg = initCommandRegistry(nil, ScannerConfig{}, ToolConfig{}, nil, "", nil, visionCfg, logger)
	if !reg.Has("vision") {
		t.Fatal("vision command should be registered when vision config is provided")
	}
}

func TestInitCommandRegistryRegistersWebToolsAlways(t *testing.T) {
	logger := telemetry.NopLogger()
	reg := initCommandRegistry(nil, ScannerConfig{}, ToolConfig{}, nil, "", nil, nil, logger)

	if !reg.Has("web_fetch") {
		t.Fatal("web_fetch command should always be registered")
	}
	if !reg.Has("web_search") {
		t.Fatal("web_search command should always be registered")
	}
}

func TestInitCommandRegistryRegistersScannerCommands(t *testing.T) {
	logger := telemetry.NopLogger()
	engines := &engine.Set{
		Gogo:  gogo.NewEngine(nil),
		Spray: spray.NewEngine(nil),
	}

	reg := initCommandRegistry(engines, ScannerConfig{}, ToolConfig{}, nil, "", nil, nil, logger)

	for _, name := range []string{"scan", "gogo", "spray"} {
		if !reg.Has(name) {
			t.Fatalf("%s command should be registered when scanner engines are available", name)
		}
	}
}

func TestInitCommandRegistryRegisters4CoreTools(t *testing.T) {
	logger := telemetry.NopLogger()
	reg := initCommandRegistry(nil, ScannerConfig{}, ToolConfig{BashTimeout: 30}, nil, "", nil, nil, logger)

	tools := reg.Tools()
	expected := map[string]bool{"read": true, "write": true, "glob": true, "bash": true}
	for _, tool := range tools {
		if !expected[tool.Name()] {
			t.Fatalf("unexpected agent tool: %s", tool.Name())
		}
	}
	if len(tools) != len(expected) {
		names := make([]string, len(tools))
		for i, tool := range tools {
			names[i] = tool.Name()
		}
		t.Fatalf("expected %d agent tools, got %d: %v", len(expected), len(tools), names)
	}
}

func TestCommandRegistryOnlyExposesCoreTrueTools(t *testing.T) {
	// Verify that pseudo-commands (web_search, web_fetch, etc.) do NOT appear
	// as agent tools — only as pseudo-commands via the bash tool.
	reg := command.NewRegistry()
	command.BuildAll(&command.Deps{
		WorkDir:     "/tmp",
		BashTimeout: 30,
	}, reg)

	for _, tool := range reg.Tools() {
		switch tool.Name() {
		case "read", "write", "glob", "bash":
			// ok — core tools
		default:
			t.Fatalf("pseudo-command %q should NOT be registered as an agent tool", tool.Name())
		}
	}
}

func TestScanVerifySystemPromptUsesFullAgentRegistry(t *testing.T) {
	logger := telemetry.NopLogger()
	reg := initCommandRegistry(nil, ScannerConfig{}, ToolConfig{BashTimeout: 30}, nil, "test-model", nil, nil, logger)

	prompt := buildScanVerifySystemPrompt(reg, nil, "")
	for _, want := range []string{
		"### bash",
		"web_search",
		"web_fetch",
		"Do not run scanner pseudo-commands",
		"Return only the exact status:/summary:/evidence: lines",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("scan verify system prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestScanVerifyBlocksRecursiveScannerCommands(t *testing.T) {
	for _, tc := range []struct {
		command string
		blocked bool
	}{
		{command: "scan -i 127.0.0.1", blocked: true},
		{command: "aiscan scan -i 127.0.0.1", blocked: true},
		{command: "spray -u https://example.test", blocked: true},
		{command: `web_search "kingdee k3 cloud cve"`, blocked: false},
		{command: "web_fetch https://example.test/advisory", blocked: false},
		{command: "echo scan", blocked: false},
	} {
		if got := scanVerifyBlocksCommand(tc.command); got != tc.blocked {
			t.Fatalf("scanVerifyBlocksCommand(%q) = %v, want %v", tc.command, got, tc.blocked)
		}
	}
}

func TestAppCloseClosesPseudoCommands(t *testing.T) {
	reg := command.NewRegistry()
	closed := false
	reg.Register(&closeRecordingCommand{closed: &closed}, "tools")

	app := &App{Commands: reg}
	app.Close()

	if !closed {
		t.Fatal("pseudo-command Close() was not called")
	}
}

type closeRecordingCommand struct {
	closed *bool
}

func (c *closeRecordingCommand) Name() string { return "closer" }

func (c *closeRecordingCommand) Usage() string { return "" }

func (c *closeRecordingCommand) Execute(context.Context, []string) (string, error) {
	return "", nil
}

func (c *closeRecordingCommand) Close() {
	*c.closed = true
}
