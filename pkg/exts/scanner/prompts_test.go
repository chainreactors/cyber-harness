package scanner

import (
	"context"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/tools/scan"
	"github.com/chainreactors/utils/parsers"
)

func scannerPromptResolver(t *testing.T, contributions ...prompt.Contribution) prompt.Resolver {
	t.Helper()
	registry := prompt.NewRegistry()
	values := append([]prompt.Contribution{scannerPromptContribution()}, contributions...)
	if _, err := registry.Add(values...); err != nil {
		t.Fatal(err)
	}
	if err := registry.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close(context.Background()) })
	return registry
}

func TestScannerWorkerPromptCanBeExtended(t *testing.T) {
	resolver := scannerPromptResolver(t, prompt.Contribution{
		Name:    "test.scanner.verify",
		Targets: []prompt.Target{scan.VerifyRequestTarget},
		Apply: func(_ context.Context, document *prompt.Document, _ prompt.Context) error {
			return document.Replace(prompt.SectionRequest, prompt.Static("custom verify request"))
		},
	})
	result := resolver.Build(t.Context(), prompt.Context{Target: scan.VerifyRequestTarget})
	if len(result.Diagnostics) != 0 || result.Prompt != "custom verify request" {
		t.Fatalf("extended verify prompt = %#v", result)
	}
}

func TestScannerOwnsWorkerPrompts(t *testing.T) {
	resolver := scannerPromptResolver(t)
	for _, target := range []prompt.Target{scan.VerifySystemTarget, scan.SniperSystemTarget} {
		result := resolver.Build(t.Context(), prompt.Context{
			Target: target, Agent: prompt.AgentContext{Instructions: "worker instructions"},
		})
		if len(result.Diagnostics) != 0 || result.Prompt != "worker instructions" {
			t.Fatalf("system target %q result = %#v", target, result)
		}
	}

	loot := parsers.Loot{
		Kind: parsers.LootVuln, Target: "https://target.example", Priority: "critical",
		Description: "remote code execution", Data: map[string]any{
			"severity": "critical", "template_id": "rce-1", "service": "https",
			"fingers": []string{"nginx", "php"},
		},
	}
	for _, test := range []struct {
		target prompt.Target
		want   []string
	}{
		{scan.VerifyRequestTarget, []string{"Verify this loot", "- Severity: critical", "- Template: rce-1", "- Service: https"}},
		{scan.SniperRequestTarget, []string{"Analyze fingerprint", "- Names: nginx, php"}},
	} {
		result := resolver.Build(t.Context(), prompt.Context{
			Target: test.target, Payload: scan.WorkerPromptPayload{Loot: loot},
		})
		if len(result.Diagnostics) != 0 {
			t.Fatalf("request target %q diagnostics = %#v", test.target, result.Diagnostics)
		}
		for _, want := range test.want {
			if !strings.Contains(result.Prompt, want) {
				t.Fatalf("request target %q missing %q:\n%s", test.target, want, result.Prompt)
			}
		}
	}
}

func TestScannerRequestPromptRejectsForeignPayload(t *testing.T) {
	result := scannerPromptResolver(t).Build(t.Context(), prompt.Context{
		Target: scan.VerifyRequestTarget, Payload: "wrong",
	})
	if result.Prompt != "" || len(result.Diagnostics) != 1 || !strings.Contains(result.Diagnostics[0].Message, "scan.WorkerPromptPayload") {
		t.Fatalf("invalid payload result = %#v", result)
	}
}
