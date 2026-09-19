package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/aop"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
	types "github.com/chainreactors/cyber/core/types"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/commands"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	profilepkg "github.com/chainreactors/cyber/pkg/profile"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
)

type scannerProvider struct{}

func (scannerProvider) Name() string { return "scanner-test" }

func (scannerProvider) ChatCompletion(context.Context, *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	return nil, errors.New("unexpected model request")
}

type scannerCommands struct{}

func (scannerCommands) Get(name string) (*types.CommandSpec, bool) {
	return &types.CommandSpec{Name: name}, name == "gogo"
}
func (scannerCommands) Has(name string) bool          { return name == "gogo" }
func (scannerCommands) All() []*types.CommandSpec     { return nil }
func (scannerCommands) Names() []string               { return []string{"gogo"} }
func (scannerCommands) DescriptionPath(string) string { return "" }
func (scannerCommands) UsageDocs() string             { return "" }
func (scannerCommands) Execute(context.Context, string, *commands.Execution) (any, error) {
	return nil, errors.New("unexpected command execution")
}
func (scannerCommands) Run(context.Context, []string, *commands.Execution) (any, error) {
	return nil, errors.New("unexpected command execution")
}

type scannerProfile struct {
	app          *apppkg.State
	runtimeErr   error
	loaded       bool
	closed       bool
	runtimeCalls int
}

func (p *scannerProfile) Load(context.Context) error { p.loaded = true; return nil }
func (p *scannerProfile) Close(context.Context) error {
	p.closed = true
	return nil
}
func (p *scannerProfile) State() (*apppkg.State, error) { return p.app, nil }
func (p *scannerProfile) Runtime() (*agentsession.Runtime, error) {
	p.runtimeCalls++
	return nil, p.runtimeErr
}
func (*scannerProfile) RegisterNamespaces(*aop.NamespaceMux) error { return nil }
func (*scannerProfile) AgentStatus() *aop.AgentStatus              { return &aop.AgentStatus{} }

// Shell is what a host publishes once its graph has loaded; this profile only
// needs it to get past the command check and reach the runtime path.
func (p *scannerProfile) Shell() (commands.Executor, *terminaltool.BashTool) {
	return scannerCommands{}, &terminaltool.BashTool{}
}
func (*scannerProfile) ConsoleBindings() *consoleapi.Bindings { return nil }

func TestLoadAgentProfileRejectsNilConstructorAndResult(t *testing.T) {
	if _, _, err := loadAgentProfile(t.Context(), nil, nil, telemetry.NopLogger(), nil); err == nil {
		t.Fatal("nil profile constructor was accepted")
	}
	newProfile := func(profilepkg.Request) (profilepkg.Profile, error) { return nil, nil }
	if _, _, err := loadAgentProfile(t.Context(), newProfile, nil, telemetry.NopLogger(), nil); err == nil {
		t.Fatal("nil profile result was accepted")
	}
}

func TestDirectScannerAIUsesProfileRuntime(t *testing.T) {
	runtimeErr := errors.New("profile runtime sentinel")
	application := &apppkg.State{}
	application.SetProvider(scannerProvider{}, provider.ProviderConfig{})
	p := &scannerProfile{app: application, runtimeErr: runtimeErr}
	var request profilepkg.Request
	newProfile := func(value profilepkg.Request) (profilepkg.Profile, error) {
		request = value
		return p, nil
	}
	option := &cfg.Option{LLMOptions: cfg.LLMOptions{AI: true}}
	err := RunDirectScannerMode(t.Context(), newProfile, option, []string{"gogo", "-i", "127.0.0.1"}, telemetry.NopLogger())
	if !errors.Is(err, runtimeErr) {
		t.Fatalf("RunDirectScannerMode() error = %v, want Profile.Runtime error", err)
	}
	if request.ProviderMode != profilepkg.ProviderRequired {
		t.Fatalf("provider mode = %v, want required", request.ProviderMode)
	}
	if request.Session == nil || request.Session.Loop == nil {
		t.Fatalf("runtime request = %#v, want scanner Agent configuration", request.Session)
	}
	if request.Session.PromptTarget != prompt.ScannerSystem || request.Session.ScannerName != "gogo" {
		t.Fatalf("scanner prompt selection = target %q scanner %q", request.Session.PromptTarget, request.Session.ScannerName)
	}
	if !p.loaded || !p.closed || p.runtimeCalls != 1 {
		t.Fatalf("profile lifecycle: loaded=%v closed=%v runtime calls=%d", p.loaded, p.closed, p.runtimeCalls)
	}
}

func TestFilterScannerJSONLinesRemovesPTYProgress(t *testing.T) {
	if !isDirectScannerJSONOutput([]string{"scan", "--json"}) {
		t.Fatal("scan --json was not recognized as direct JSON output")
	}
	raw := "" +
		"╭─ scanner ─╮\n" +
		"[summary] completed 1 target\n" +
		`{"url":"http://127.0.0.1:18080","status":200}` + "\n" +
		`{"url":"http://127.0.0.1:18080/admin/","status":403}` + "\n"
	got := filterScannerJSONLines(raw)
	if strings.Contains(got, "summary") || strings.Contains(got, "scanner") {
		t.Fatalf("progress leaked into JSON output: %q", got)
	}
	if want := "{\"url\":\"http://127.0.0.1:18080\",\"status\":200}\n{\"url\":\"http://127.0.0.1:18080/admin/\",\"status\":403}\n"; got != want {
		t.Fatalf("filtered JSON = %q, want %q", got, want)
	}
}
