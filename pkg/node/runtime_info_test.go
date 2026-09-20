package node

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/skills"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	apppkg "github.com/chainreactors/cyber/pkg/app"
)

func TestAgentStatusIncludesLLMHealthFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized\ninvalid API key", http.StatusUnauthorized)
	}))
	defer server.Close()
	app := &apppkg.State{}
	if _, _, err := app.ReloadProvider(context.Background(), agent.ProviderConfig{
		Provider: "openai", Model: "gpt-test", BaseURL: server.URL + "/v1", APIKey: "test",
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

func TestCommandSpecsIncludeNodeRegistryCommands(t *testing.T) {
	registry := hosttest.Commands(t,
		coretool.Command{
			Name: "gogo", Usage: "Usage:\n  gogo [OPTIONS]",
			DescriptionPath: "cyber://skills/cyber/okf/easm/gogo.md",
			Run:             func(context.Context, *coretool.Execution) (any, error) { return nil, nil },
		}, coretool.Command{
			Name: "tmux", Usage: "Usage: tmux <action>",
			DescriptionPath: "cyber://skills/cyber/okf/runtime/tmux.md",
			Run:             func(context.Context, *coretool.Execution) (any, error) { return nil, nil },
		})
	store, diagnostics := skills.LoadEmbeddedStore()
	if len(diagnostics) != 0 {
		t.Fatalf("load embedded skills diagnostics = %+v", diagnostics)
	}

	resource, err := session.NewResource(session.Config{State: &apppkg.State{}, CommandRegistry: registry, Skills: store})
	if err != nil {
		t.Fatal(err)
	}
	runtime := resource.Runtime()
	catalog := CommandSpecs(runtime)
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
	if got["!tmux"].description != "PTY session manager built into cyber. Bash commands stay foreground by default and move to background only when the agent sets wait." {
		t.Fatalf("!tmux description = %q", got["!tmux"].description)
	}
}

func TestCommandSpecsMissingDescriptionPathStayVisible(t *testing.T) {
	registry := hosttest.Commands(t, coretool.Command{Name: "custom", Usage: "custom", Run: func(context.Context, *coretool.Execution) (any, error) { return nil, nil }})
	catalog := RegistryCommandSpecs(registry, nil)
	for _, spec := range catalog {
		if spec.GetName() == "!custom" && spec.GetDescription() != "" {
			t.Fatalf("custom description = %q, want empty so the UI exposes the missing OKF declaration", spec.GetDescription())
		}
	}
}
