package prompt

import (
	"context"
	"strings"
	"testing"

	agentprompt "github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/core/extension"
)

func loadResolver(t *testing.T, contributions ...agentprompt.Contribution) agentprompt.Resolver {
	t.Helper()
	var resolver agentprompt.Resolver
	values := []extension.Extension{New()}
	if len(contributions) != 0 {
		values = append(values, extension.Func{LoadFunc: func(scope *extension.Scope) error {
			return extension.Add(scope, contributions...)
		}})
	}
	values = append(values, extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		resolver, err = extension.Use[agentprompt.Resolver](scope)
		return err
	}})
	set, err := extension.New(values...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	return resolver
}

func buildDefaultPrompt(t *testing.T, input agentprompt.Context) string {
	t.Helper()
	result := loadResolver(t).Build(t.Context(), input)
	if len(result.Diagnostics) != 0 {
		t.Fatalf("prompt diagnostics = %#v", result.Diagnostics)
	}
	return result.Prompt
}

func TestDefaultResolverCoversEveryPromptTarget(t *testing.T) {
	resolver := loadResolver(t)
	inputs := []agentprompt.Context{
		{Target: agentprompt.MainSystem},
		{Target: agentprompt.EvaluatorSystem},
		{Target: agentprompt.EvaluatorRequest, Evaluation: agentprompt.EvaluationContext{Goal: "goal", Trace: "trace"}},
		{Target: agentprompt.CompactSystem},
		{Target: agentprompt.CompactRequest},
		{Target: agentprompt.CompactPrefix},
	}
	for _, input := range inputs {
		result := resolver.Build(t.Context(), input)
		if result.Prompt == "" || len(result.Diagnostics) != 0 {
			t.Errorf("target %q result = %#v", input.Target, result)
		}
	}
}

func TestExternalContributionCanRewriteBuiltInPrompt(t *testing.T) {
	resolver := loadResolver(t, agentprompt.Contribution{
		Name: "test.rewrite", Targets: []agentprompt.Target{agentprompt.MainSystem},
		Apply: func(_ context.Context, document *agentprompt.Document, _ agentprompt.Context) error {
			if err := document.Replace(agentprompt.SectionIdentity, agentprompt.Static("custom identity")); err != nil {
				return err
			}
			document.Remove(agentprompt.SectionAuthorization)
			return document.After(agentprompt.SectionIdentity, "custom.policy", agentprompt.Static("custom policy"))
		},
	})
	result := resolver.Build(t.Context(), agentprompt.Context{Target: agentprompt.MainSystem})
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
	if !strings.HasPrefix(result.Prompt, "custom identity\n\ncustom policy") {
		t.Fatalf("prompt was not rewritten:\n%s", result.Prompt)
	}
	if strings.Contains(result.Prompt, "## Authorization Context") {
		t.Fatalf("removed section remains:\n%s", result.Prompt)
	}
}

func TestDefaultAgentPrompt(t *testing.T) {
	result := buildDefaultPrompt(t, agentprompt.Context{Target: agentprompt.MainSystem})
	for _, want := range []string{
		"operating inside a cyber-harness runtime",
		"## Environment",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("prompt missing %q:\n%s", want, result)
		}
	}
	// The neutral skeleton carries no scanner content; the scanner extension
	// contributes identity, authorization, commands and principles itself.
	for _, unwanted := range []string{
		"Authorization Context",
		"Pseudo-Commands",
		"Key Principles",
		"Scanner Agent Constraints",
		"benchmark",
	} {
		if strings.Contains(result, unwanted) {
			t.Fatalf("neutral prompt contains scanner content %q:\n%s", unwanted, result)
		}
	}
	if strings.Contains(result, "## Available Tools") {
		t.Fatal("prompt contains tools section without tools")
	}
}

func TestDefaultPromptRendersSkills(t *testing.T) {
	result := buildDefaultPrompt(t, agentprompt.Context{
		Target: agentprompt.MainSystem,
		Agent: agentprompt.AgentContext{
			Skills: []agentprompt.Skill{{
				Name: "cyber", Description: "Security workflows", Location: "cyber://skills/cyber/SKILL.md",
			}},
			LoadedSkills: []agentprompt.LoadedSkill{{
				Name: "scan/verify", Body: "Verify high-priority findings.",
			}},
		},
	})
	for _, want := range []string{
		"<available_skills>",
		"<name>cyber</name>",
		"cyber://skills/cyber/SKILL.md",
		"## Skill: scan/verify",
		"Verify high-priority findings.",
	} {
		if !strings.Contains(result, want) {
			t.Fatalf("prompt missing %q:\n%s", want, result)
		}
	}
	if strings.Index(result, "## Skill: scan/verify") < strings.Index(result, "</available_skills>") {
		t.Fatal("loaded skills should appear after the available skills list")
	}
}

func TestDefaultPromptEscapesSkillMetadata(t *testing.T) {
	result := buildDefaultPrompt(t, agentprompt.Context{
		Target: agentprompt.MainSystem,
		Agent: agentprompt.AgentContext{Skills: []agentprompt.Skill{{
			Name: "a&b", Description: "use <carefully>", Location: "cyber://skills/a?x=1&y=2",
		}}},
	})
	for _, want := range []string{"<name>a&amp;b</name>", "use &lt;carefully&gt;", "x=1&amp;y=2"} {
		if !strings.Contains(result, want) {
			t.Fatalf("prompt missing escaped metadata %q:\n%s", want, result)
		}
	}
}
