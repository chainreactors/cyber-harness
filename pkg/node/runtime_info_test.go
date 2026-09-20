package node

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	promptext "github.com/chainreactors/cyber/pkg/exts/prompt"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"

	"github.com/chainreactors/cyber/internal/testutil/apptest"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/session"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
)

func TestAgentStatusIncludesLLMHealthFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized\ninvalid API key", http.StatusUnauthorized)
	}))
	defer server.Close()
	app := apptest.NewFixture(t, nil, nil)
	if _, _, err := app.Providers.Reload(context.Background(), agent.ProviderConfig{
		Provider: "openai", Model: "gpt-test", BaseURL: server.URL + "/v1", APIKey: "test",
	}, nil); err != nil {
		t.Fatal(err)
	}
	status := AgentStatus(app.Providers)
	if status.GetProvider() != "openai" || status.GetModel() != "gpt-test" {
		t.Fatalf("status provider/model = %+v", status)
	}
	if !strings.Contains(status.GetConfigError(), "unauthorized invalid API key") {
		t.Fatalf("config error = %q", status.GetConfigError())
	}
}

func TestCommandSpecsIncludeNodeRegistryCommands(t *testing.T) {
	f := apptest.NewFixture(t, nil, nil)
	installed := sessionext.New(session.Config{})
	hosttest.Load(t, t.Context(), append(apptest.Entries(t, f),
		promptext.New(), loopext.New(agent.NoLoop()),
		extension.Func{LoadFunc: func(scope *extension.Scope) error {
			return extension.Add(scope,
				coretool.Command{Name: "gogo", Usage: "Usage: gogo [OPTIONS]", DescriptionPath: "cyber://skills/cyber/okf/easm/gogo.md", Run: func(context.Context, *coretool.Execution) (any, error) { return nil, nil }},
			)
		}}, installed)...)
	runtime := installed.Runtime()
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
	if got["!tmux"] == nil || got["!tmux"].usage != "!tmux - PTY session manager" {
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
