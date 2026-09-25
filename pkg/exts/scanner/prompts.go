package scanner

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/tools/scan"
	"github.com/chainreactors/utils/parsers"
)

func scannerPromptContribution() prompt.Contribution {
	return prompt.Contribution{
		Name: "scanner.prompts",
		Targets: []prompt.Target{
			scan.VerifySystemTarget,
			scan.VerifyRequestTarget,
			scan.SniperSystemTarget,
			scan.SniperRequestTarget,
		},
		Apply: func(_ context.Context, document *prompt.Document, input prompt.Context) error {
			switch input.Target {
			case scan.VerifySystemTarget, scan.SniperSystemTarget:
				return document.Add(prompt.SectionInstructions, func(_ context.Context, input prompt.Context) (string, error) {
					return input.Agent.Instructions, nil
				})
			case scan.VerifyRequestTarget:
				return document.Add(prompt.SectionRequest, renderVerifyRequest)
			case scan.SniperRequestTarget:
				return document.Add(prompt.SectionRequest, renderSniperRequest)
			default:
				return fmt.Errorf("unsupported scanner prompt target %q", input.Target)
			}
		},
	}
}

func renderVerifyRequest(_ context.Context, input prompt.Context) (string, error) {
	loot, err := scannerPromptPayload(input)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Verify this loot on target %s:\n\n", loot.Target)
	fmt.Fprintf(&out, "- Kind: %s\n", loot.Kind)
	fmt.Fprintf(&out, "- Priority: %s\n", loot.Priority)
	fmt.Fprintf(&out, "- Description: %s\n", loot.Description)
	if severity, ok := loot.Data["severity"].(string); ok {
		fmt.Fprintf(&out, "- Severity: %s\n", severity)
	}
	if templateID, ok := loot.Data["template_id"].(string); ok {
		fmt.Fprintf(&out, "- Template: %s\n", templateID)
	}
	if service, ok := loot.Data["service"].(string); ok {
		fmt.Fprintf(&out, "- Service: %s\n", service)
	}
	return out.String(), nil
}

func renderSniperRequest(_ context.Context, input prompt.Context) (string, error) {
	loot, err := scannerPromptPayload(input)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Analyze fingerprint on target %s:\n\n", loot.Target)
	fmt.Fprintf(&out, "- Fingerprints: %s\n", loot.Description)
	if fingers, ok := loot.Data["fingers"].([]string); ok {
		fmt.Fprintf(&out, "- Names: %s\n", strings.Join(fingers, ", "))
	}
	return out.String(), nil
}

func scannerPromptPayload(input prompt.Context) (parsers.Loot, error) {
	payload, ok := input.Payload.(parsers.Loot)
	if !ok {
		return parsers.Loot{}, fmt.Errorf("scanner prompt target %q requires parsers.Loot", input.Target)
	}
	return payload, nil
}
