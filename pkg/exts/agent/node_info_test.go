package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/internal/extensiontest"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/skills"
)

func TestAgentStatusIncludesLLMHealthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized\ninvalid API key", http.StatusUnauthorized)
	}))
	defer srv.Close()
	app := &apppkg.App{}
	if _, _, err := app.ReloadProvider(context.Background(), agent.ProviderConfig{
		Provider: "openai", Model: "gpt-test", BaseURL: srv.URL + "/v1", APIKey: "test",
	}); err != nil {
		t.Fatal(err)
	}
	status := AgentStatus(app)
	if status.GetProvider() != "openai" || status.GetModel() != "gpt-test" {
		t.Fatalf("status provider/model = %+v", status)
	}
	if !strings.Contains(status.GetConfigError(), "unauthorized invalid API key") {
		t.Fatalf("config error = %q", status.GetConfigError())
	}
}

func TestCommandCatalogIncludesNodeRegistryCommands(t *testing.T) {
	registry := extensiontest.Commands(t, "commands",
		commands.Command{
			Name: "gogo", Usage: "Usage:\n  gogo [OPTIONS]",
			DescriptionPath: "aiscan://skills/aiscan/okf/easm/gogo.md",
			Run:             func(context.Context, *commands.Execution) (any, error) { return nil, nil },
		}, commands.Command{
			Name: "tmux", Usage: "Usage: tmux <action>",
			DescriptionPath: "aiscan://skills/aiscan/okf/runtime/tmux.md",
			Run:             func(context.Context, *commands.Execution) (any, error) { return nil, nil },
		})
	store, diagnostics := skills.LoadEmbeddedStore()
	if len(diagnostics) != 0 {
		t.Fatalf("load embedded skills diagnostics = %+v", diagnostics)
	}

	runtime := &Runtime{app: &apppkg.App{Commands: registry, Skills: store}, commands: builtinCommands()}
	catalog := runtime.CommandCatalog()
	got := make(map[string]*struct{ usage, description string }, len(catalog))
	for _, spec := range catalog {
		got[spec.GetName()] = &struct{ usage, description string }{spec.GetUsage(), spec.GetDescription()}
	}
	if got["!gogo"] == nil || got["!gogo"].usage != "!gogo [OPTIONS]" {
		t.Fatalf("!gogo = %+v", got["!gogo"])
	}
	if got["!gogo"].description != "Use this playbook when working with gogo for host, port, service, banner, fingerprint, or vulnerability-hint discovery." {
		t.Fatalf("!gogo description = %q", got["!gogo"].description)
	}
	if got["!tmux"] == nil || got["!tmux"].usage != "!tmux <action>" {
		t.Fatalf("!tmux = %+v", got["!tmux"])
	}
	if got["!tmux"].description != "PTY session manager built into aiscan. Bash commands stay foreground by default and move to background only when the agent sets wait." {
		t.Fatalf("!tmux description = %q", got["!tmux"].description)
	}
}

func TestCommandCatalogMissingDescriptionPathStaysVisible(t *testing.T) {
	registry := extensiontest.Commands(t, "custom", commands.Command{Name: "custom", Usage: "custom", Run: func(context.Context, *commands.Execution) (any, error) { return nil, nil }})
	catalog := RegistryCommandCatalog(registry, nil)
	for _, spec := range catalog {
		if spec.GetName() == "!custom" && spec.GetDescription() != "" {
			t.Fatalf("custom description = %q, want empty so the UI exposes the missing OKF declaration", spec.GetDescription())
		}
	}
}
