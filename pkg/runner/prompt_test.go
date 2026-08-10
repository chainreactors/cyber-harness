package runner

import (
	"context"
	"strings"
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/skills"
)

func TestBuildSystemPromptIncludesSkills(t *testing.T) {
	tools := commands.NewRegistry()
	loaded, diagnostics := skills.LoadEmbedded()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}

	prompt := BuildSystemPrompt(&PromptConfig{
		Tools:  tools,
		Skills: loaded,
	}, nil)
	for _, want := range []string{
		"## Available Skills",
		"<available_skills>",
		"<name>aiscan</name>",
		"aiscan://skills/aiscan/SKILL.md",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, internal := range []string{"scan", "gogo", "spray", "katana", "fuzz", "zombie", "neutron"} {
		if strings.Contains(prompt, "<name>"+internal+"</name>") {
			t.Fatalf("prompt includes internal skill %q:\n%s", internal, prompt)
		}
	}
}

func TestBuildSystemPromptAllowsNilConfig(t *testing.T) {
	prompt := BuildSystemPrompt(nil, nil)
	for _, want := range []string{
		"AIScan, a Cyber Harness for model companies",
		"Use a hacker's mindset throughout",
		"challenge the target's assumptions",
		"examine trust boundaries and state transitions",
		"paths that turn weaknesses into meaningful impact",
		"## Authorization Context",
		"source code, binaries, artifacts, credentials, datasets",
		"## Environment",
		"## Key Principles",
		"Treat hypotheses as provisional",
		"Distinguish observed facts, reasoned inferences, and unverified leads",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, unwanted := range []string{
		"autonomous security assessment agent",
		"Read aiscan://skills/aiscan/SKILL.md for execution rules",
		"penetration testing, reverse engineering, adversarial tasks, and code auditing",
	} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("prompt contains obsolete global scanning guidance %q:\n%s", unwanted, prompt)
		}
	}
}

func TestBuildSystemPromptScannerAgentUsesCyberHarnessIdentity(t *testing.T) {
	prompt := BuildSystemPrompt(&PromptConfig{
		ScannerAgentMode: true,
		ScannerName:      "gogo",
	}, nil)

	for _, want := range []string{
		"gogo analysis agent inside AIScan, a Cyber Harness",
		"Execute the requested scanner command using the bash tool",
		"## Authorization Context",
		"## Scanner Agent Constraints",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("scanner prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestSystemPromptFuncAdaptsToTools(t *testing.T) {
	cfg := &PromptConfig{}
	fn := SystemPromptFunc(cfg)

	result := fn(nil)
	if strings.Contains(result, "## Available Tools") {
		t.Fatal("should not have tools section with empty registry")
	}
}

func TestBuildSystemPromptLoadsSkillBody(t *testing.T) {
	prompt := BuildSystemPrompt(&PromptConfig{
		LoadedSkills: []LoadedSkill{
			{Name: "scan/verify", Body: "Verify all high-priority findings with active probing."},
			{Name: "scan/sniper", Body: "Search public CVEs for fingerprints."},
		},
	}, nil)

	for _, want := range []string{
		"## Skill: scan/verify",
		"Verify all high-priority findings with active probing.",
		"## Skill: scan/sniper",
		"Search public CVEs for fingerprints.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	// Loaded skills should appear before Key Principles
	skillIdx := strings.Index(prompt, "## Skill: scan/verify")
	principlesIdx := strings.Index(prompt, "## Key Principles")
	if skillIdx > principlesIdx {
		t.Fatal("loaded skills should appear before principles")
	}
}

func TestAgentRuntimePreloadsBaseSkillOnce(t *testing.T) {
	for _, tc := range []struct {
		name   string
		skills []string
	}{
		{name: "default"},
		{name: "explicit duplicate", skills: []string{"aiscan"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			option := &cfg.Option{}
			option.Skills = tc.skills
			rt, err := NewAgentRuntime(context.Background(), option, telemetry.NopLogger(), &RuntimeConfig{
				ProviderOptional: true,
			})
			if err != nil {
				t.Fatalf("NewAgentRuntime() error = %v", err)
			}
			defer rt.Close()

			if count := strings.Count(rt.systemPrompt, "## Skill: aiscan"); count != 1 {
				t.Fatalf("base skill count = %d, want 1", count)
			}
			for _, want := range []string{
				"## User Tool Restrictions",
				"## Skill: aiscan",
				"# AIScan ASM and Penetration Testing",
				"must not redirect tasks outside its scope into scanning",
				"## Tool Invocation Rules",
				"## Verification Standard",
				"## Evidence & Findings",
			} {
				if !strings.Contains(rt.systemPrompt, want) {
					t.Fatalf("system prompt missing base skill rule %q", want)
				}
			}
			for _, unwanted := range []string{
				"## Fingerprint → POC Workflow",
				"## Asset Triage",
				"## Post-Scan Analysis",
				"map the application before focused testing",
			} {
				if strings.Contains(rt.systemPrompt, unwanted) {
					t.Fatalf("system prompt contains SOP guidance %q", unwanted)
				}
			}
		})
	}
}
