package scanner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/tools/scan"
)

// goldenPromptContext is the fixed render input shared with the golden files
// captured before the cyber prompt content moved into this extension.
func goldenPromptContext(target prompt.Target) prompt.Context {
	return prompt.Context{
		Target: target,
		Agent: prompt.AgentContext{
			Name: "cyber", Model: "test-model", NodeName: "node-1",
			CommandName: "gogo",
			OS: "linux", Arch: "amd64", Hostname: "test-host",
			Now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Windows: false,
			Tools:        []prompt.Tool{{Name: "bash", Description: "Run commands"}, {Name: "read", Description: "Read files"}},
			CommandDocs:  "gogo - port scanner\n  -i target",
			Skills:       []prompt.Skill{{Name: "cyber", Description: "Security workflows", Location: "cyber://skills/cyber/SKILL.md"}},
			LoadedSkills: []prompt.LoadedSkill{{Name: "scan/verify", Body: "Verify findings."}},
		},
	}
}

// TestCyberPromptMatchesGolden pins the scanner-owned prompt sections to the
// output the shared prompt extension produced before the content moved here.
// Regenerate the golden files only when the change is intentional.
func TestCyberPromptMatchesGolden(t *testing.T) {
	resolver := installScanner(t, t.TempDir(), Config{}).prompts
	for _, tc := range []struct {
		name   string
		target prompt.Target
	}{
		{"prompt_main_system", prompt.MainSystem},
		{"prompt_scanner_system", scan.ScannerSystemTarget},
	} {
		result := resolver.Build(t.Context(), goldenPromptContext(tc.target))
		if len(result.Diagnostics) != 0 {
			t.Fatalf("%s diagnostics = %#v", tc.name, result.Diagnostics)
		}
		golden, err := os.ReadFile(filepath.Join("testdata", tc.name+".golden"))
		if err != nil {
			t.Fatal(err)
		}
		if result.Prompt != string(golden) {
			t.Errorf("%s drifted from golden %s.golden;\n--- got ---\n%s\n--- want ---\n%s", tc.name, tc.name, result.Prompt, string(golden))
		}
	}
}
