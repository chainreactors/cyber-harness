package runner

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/skills"
)

func TestCommandCatalogIncludesNodeRegistryCommands(t *testing.T) {
	registry := commands.NewRegistry()
	registry.Register(commands.Command{
		Name: "gogo", Usage: "Usage:\n  gogo [OPTIONS]",
		DescriptionPath: "aiscan://skills/aiscan/okf/easm/gogo.md",
		Run:             func(context.Context, *commands.Execution) (any, error) { return nil, nil },
	}, "scanner")
	registry.Register(commands.Command{
		Name: "tmux", Usage: "Usage: tmux <action>",
		DescriptionPath: "aiscan://skills/aiscan/okf/runtime/tmux.md",
		Run:             func(context.Context, *commands.Execution) (any, error) { return nil, nil },
	}, "core")
	store, diagnostics := skills.LoadEmbeddedStore()
	if len(diagnostics) != 0 {
		t.Fatalf("load embedded skills diagnostics = %+v", diagnostics)
	}

	catalog := CommandCatalog(&App{Commands: registry, Skills: store})
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
	if got["!tmux"].description != "PTY session manager built into aiscan. All bash commands run inside tmux sessions; long commands auto-background with inbox delivery." {
		t.Fatalf("!tmux description = %q", got["!tmux"].description)
	}
}

func TestCommandCatalogMissingDescriptionPathStaysVisible(t *testing.T) {
	registry := commands.NewRegistry()
	registry.Register(commands.Command{Name: "custom", Usage: "custom", Run: func(context.Context, *commands.Execution) (any, error) { return nil, nil }}, "custom")
	catalog := RegistryCommandCatalog(registry, nil)
	for _, spec := range catalog {
		if spec.GetName() == "!custom" && spec.GetDescription() != "" {
			t.Fatalf("custom description = %q, want empty so the UI exposes the missing OKF declaration", spec.GetDescription())
		}
	}
}
