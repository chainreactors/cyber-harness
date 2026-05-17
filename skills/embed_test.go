package skills

import (
	"strings"
	"testing"
)

func TestLoadEmbeddedSkills(t *testing.T) {
	loaded, diagnostics := LoadEmbedded()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(loaded) < 12 {
		t.Fatalf("skills = %d, want >= 12: %#v", len(loaded), loaded)
	}

	store := NewStore(loaded)
	for _, name := range []string{"aiscan", "browser", "scan", "gogo", "spray", "zombie", "neutron", "web_search", "web_fetch", "vision", "parse_results", "filter_results", "verify", "sniper", "report"} {
		if _, ok := store.ByName(name); !ok {
			t.Fatalf("missing %s", name)
		}
	}
	skill, ok := store.ByName("aiscan")
	if !ok {
		t.Fatal("missing aiscan")
	}
	if skill.Description == "" {
		t.Fatal("description is empty")
	}
	if skill.Location != "aiscan://skills/aiscan/SKILL.md" {
		t.Fatalf("location = %q", skill.Location)
	}
	if strings.Contains(skill.Body, "---") {
		t.Fatalf("body contains frontmatter: %q", skill.Body)
	}
}

func TestFormatForPrompt(t *testing.T) {
	loaded, _ := LoadEmbedded()
	prompt := FormatForPrompt(loaded)
	for _, want := range []string{
		"<available_skills>",
		"<name>aiscan</name>",
		"aiscan://skills/aiscan/SKILL.md",
		"Use the read tool to load a skill file",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, internal := range []string{"browser", "scan", "gogo", "spray", "zombie", "neutron", "web_search", "web_fetch", "vision", "parse_results", "filter_results"} {
		if strings.Contains(prompt, "<name>"+internal+"</name>") {
			t.Fatalf("prompt includes internal skill %q:\n%s", internal, prompt)
		}
	}

	hidden := []Skill{{
		Name:        "hidden",
		Description: "hidden skill",
		Location:    "aiscan://skills/hidden/SKILL.md",
		Internal:    true,
	}}
	if got := FormatForPrompt(hidden); got != "" {
		t.Fatalf("hidden prompt = %q, want empty", got)
	}
}

func TestExpandCommand(t *testing.T) {
	store, diagnostics := LoadEmbeddedStore()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}

	expanded := ExpandCommand("/skill:gogo gogo -i 127.0.0.1 -p 80", store)
	for _, want := range []string{
		`<skill name="gogo" location="aiscan://skills/gogo/SKILL.md">`,
		"References are relative to aiscan://skills/gogo.",
		"# Gogo",
		"gogo -i 127.0.0.1 -p 80",
	} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded missing %q:\n%s", want, expanded)
		}
	}
	if strings.Contains(expanded, "---") {
		t.Fatalf("expanded contains frontmatter:\n%s", expanded)
	}

	unknown := "/skill:unknown scan"
	if got := ExpandCommand(unknown, store); got != unknown {
		t.Fatalf("unknown expansion = %q, want original", got)
	}
}

func TestReadVirtual(t *testing.T) {
	store, _ := LoadEmbeddedStore()
	content, handled, err := store.ReadVirtual("aiscan://skills/aiscan/SKILL.md")
	if err != nil {
		t.Fatalf("ReadVirtual() error = %v", err)
	}
	if !handled {
		t.Fatal("ReadVirtual() handled = false")
	}
	if !strings.Contains(content, "name: aiscan") || !strings.Contains(content, "# Aiscan Mechanisms") {
		t.Fatalf("unexpected content:\n%s", content)
	}

	_, handled, err = store.ReadVirtual("aiscan://skills/missing/SKILL.md")
	if !handled || err == nil {
		t.Fatalf("missing handled=%v err=%v, want handled error", handled, err)
	}
}
