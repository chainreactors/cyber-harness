package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var baseExpectedSkills = []string{"cyber"}

func expectedEmbeddedSkillNames() []string {
	return append([]string(nil), baseExpectedSkills...)
}

func TestLoadEmbeddedSkills(t *testing.T) {
	loaded, diagnostics := LoadEmbedded()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	expected := expectedEmbeddedSkillNames()
	if len(loaded) < len(expected) {
		t.Fatalf("skills = %d, want at least %d: %#v", len(loaded), len(expected), loaded)
	}

	store := NewStore(loaded)
	for _, name := range expected {
		if _, ok := store.ByName(name); !ok {
			t.Fatalf("missing %s", name)
		}
	}
	skill, ok := store.ByName("cyber")
	if !ok {
		t.Fatal("missing cyber")
	}
	if skill.Description == "" {
		t.Fatal("description is empty")
	}
	for _, want := range []string{"attack surface management", "penetration-testing"} {
		if !strings.Contains(skill.Description, want) {
			t.Fatalf("description missing %q: %q", want, skill.Description)
		}
	}
	if skill.Location != "cyber://skills/cyber/SKILL.md" {
		t.Fatalf("location = %q", skill.Location)
	}
	body := store.ReadBody("cyber")
	if body == "" {
		t.Fatal("ReadBody returned empty")
	}
	for _, want := range []string{
		"# Cyber ASM and Penetration Testing",
		"## General Execution Tools",
		"## ASM and Penetration Tools",
		"## Tool Invocation Rules",
		"## Verification Standard",
		"## Report Generation",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("ReadBody missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"## Fingerprint → POC Workflow",
		"## Asset Triage",
		"## Post-Scan Analysis",
	} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("ReadBody contains SOP guidance %q", unwanted)
		}
	}
	if strings.Contains(body, "---") {
		t.Fatalf("ReadBody contains frontmatter: %q", body)
	}
}

func TestExpandCommand(t *testing.T) {
	store, diagnostics := LoadEmbeddedStore()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}

	expanded := ExpandCommand("/skill:cyber check this target", store)
	for _, want := range []string{
		`<skill name="cyber" location="cyber://skills/cyber/SKILL.md">`,
		"References are relative to cyber://skills/cyber.",
		"# Cyber ASM and Penetration Testing",
		"check this target",
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
	content, handled, err := store.ReadVirtual("cyber://skills/cyber/SKILL.md")
	if err != nil {
		t.Fatalf("ReadVirtual() error = %v", err)
	}
	if !handled {
		t.Fatal("ReadVirtual() handled = false")
	}
	if !strings.Contains(content, "name: cyber") || !strings.Contains(content, "# Cyber ASM and Penetration Testing") {
		t.Fatalf("unexpected content:\n%s", content)
	}

	_, handled, err = store.ReadVirtual("cyber://skills/missing/SKILL.md")
	if !handled || err == nil {
		t.Fatalf("missing handled=%v err=%v, want handled error", handled, err)
	}
}

func TestReadVirtualOKFConcept(t *testing.T) {
	store, _ := LoadEmbeddedStore()
	content, handled, err := store.ReadVirtual("cyber://skills/cyber/okf/easm/gogo.md")
	if err != nil || !handled {
		t.Fatalf("ReadVirtual(easm/gogo) handled=%v err=%v", handled, err)
	}
	if !strings.Contains(content, "type: Tool Playbook") || !strings.Contains(content, "# Gogo") {
		t.Fatalf("unexpected concept content:\n%s", content)
	}

	body, handled, err := store.ReadVirtualBody("cyber://skills/cyber/okf/easm/gogo.md")
	if err != nil || !handled {
		t.Fatalf("ReadVirtualBody(easm/gogo) handled=%v err=%v", handled, err)
	}
	if strings.Contains(body, "---") || !strings.Contains(body, "# Gogo") {
		t.Fatalf("ReadVirtualBody should strip frontmatter:\n%s", body)
	}

	_, handled, err = store.ReadVirtual("cyber://skills/cyber/okf/easm/missing.md")
	if !handled || err == nil {
		t.Fatalf("missing concept handled=%v err=%v, want handled error", handled, err)
	}
}

func TestParseFrontmatterYAML(t *testing.T) {
	raw := "---\nname: test-skill\ndescription: A test skill\ninternal: true\nagent: true\nagent_max_turns: 5\nagent_model: gpt-4\nagent_background: true\n---\n# Body\nHello"
	fm, body := ParseFrontmatter(raw)
	if fm.Name != "test-skill" {
		t.Fatalf("name = %q", fm.Name)
	}
	if fm.Description != "A test skill" {
		t.Fatalf("description = %q", fm.Description)
	}
	if !fm.Internal {
		t.Fatal("internal should be true")
	}
	if !fm.Agent {
		t.Fatal("agent should be true")
	}
	if fm.AgentMaxTurns != 5 {
		t.Fatalf("agent_max_turns = %d", fm.AgentMaxTurns)
	}
	if fm.AgentModel != "gpt-4" {
		t.Fatalf("agent_model = %q", fm.AgentModel)
	}
	if !fm.AgentBackground {
		t.Fatal("agent_background should be true")
	}
	if !strings.Contains(body, "# Body") {
		t.Fatalf("body = %q", body)
	}
}

func TestParseFrontmatterQuotedValues(t *testing.T) {
	raw := "---\nname: \"quoted-name\"\ndescription: 'single quoted'\n---\nBody"
	fm, _ := ParseFrontmatter(raw)
	if fm.Name != "quoted-name" {
		t.Fatalf("name = %q, want quoted-name", fm.Name)
	}
	if fm.Description != "single quoted" {
		t.Fatalf("description = %q", fm.Description)
	}
}

func TestParseFrontmatterNoFrontmatter(t *testing.T) {
	raw := "# Just a body\nNo frontmatter here"
	fm, body := ParseFrontmatter(raw)
	if fm.Name != "" || fm.Description != "" {
		t.Fatalf("expected empty frontmatter, got %+v", fm)
	}
	if body != raw {
		t.Fatalf("body = %q", body)
	}
}

func TestLoadFromDir(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: my-skill\ndescription: A local skill\n---\n# My Skill\nLocal body"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, diags := LoadFromDir(dir, SourceProject)
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %#v", diags)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded = %d, want 1", len(loaded))
	}
	s := loaded[0]
	if s.Name != "my-skill" {
		t.Fatalf("name = %q", s.Name)
	}
	if s.Source != SourceProject {
		t.Fatalf("source = %q", s.Source)
	}
	if s.Location != filepath.Join(skillDir, "SKILL.md") {
		t.Fatalf("location = %q", s.Location)
	}
	if s.BaseDir != skillDir {
		t.Fatalf("baseDir = %q", s.BaseDir)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "custom.md")
	content := "---\nname: custom\ndescription: A custom skill\n---\n# Custom\nBody here"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	skill, diags, ok := LoadFromFile(filePath)
	if !ok {
		t.Fatalf("LoadFromFile failed: %#v", diags)
	}
	if skill.Name != "custom" {
		t.Fatalf("name = %q", skill.Name)
	}
	if skill.Source != SourceCLI {
		t.Fatalf("source = %q", skill.Source)
	}
	if skill.Location != filePath {
		t.Fatalf("location = %q", skill.Location)
	}
	if skill.BaseDir != dir {
		t.Fatalf("baseDir = %q", skill.BaseDir)
	}
}

func TestLoadFromFileDefaultsName(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "my-thing.md")
	content := "---\ndescription: No explicit name\n---\n# Body"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	skill, _, ok := LoadFromFile(filePath)
	if !ok {
		t.Fatal("LoadFromFile failed")
	}
	if skill.Name != "my-thing" {
		t.Fatalf("name = %q, want my-thing", skill.Name)
	}
}

func TestOverrideEmbeddedWithLocal(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "cyber")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: cyber\ndescription: Overridden cyber skill\n---\n# Overridden\nLocal override body"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	embedded, _ := LoadEmbedded()
	local, _ := LoadFromDir(dir, SourceProject)
	all := append(embedded, local...)
	store := newStoreWithOverride(all)

	skill, ok := store.ByName("cyber")
	if !ok {
		t.Fatal("missing cyber")
	}
	if skill.Source != SourceProject {
		t.Fatalf("source = %q, want project (override)", skill.Source)
	}
	if skill.Description != "Overridden cyber skill" {
		t.Fatalf("description = %q", skill.Description)
	}
	body := store.ReadBody("cyber")
	if !strings.Contains(body, "Local override body") {
		t.Fatalf("body = %q, want local override", body)
	}
}

func TestStoreReadBodyLocal(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "local-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: local-skill\ndescription: A local skill\n---\n# Local\nLocal body content"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	local, _ := LoadFromDir(dir, SourceProject)
	store := NewStore(local)
	body := store.ReadBody("local-skill")
	if !strings.Contains(body, "Local body content") {
		t.Fatalf("body = %q", body)
	}
}

func TestReadVirtualLocal(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "local-virt")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillContent := "---\nname: local-virt\ndescription: Virtual local\n---\n# Body"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "extra.md"), []byte("extra content"), 0o644); err != nil {
		t.Fatal(err)
	}

	local, _ := LoadFromDir(dir, SourceProject)
	store := NewStore(local)

	content, handled, err := store.ReadVirtual(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("ReadVirtual error = %v", err)
	}
	if !handled {
		t.Fatal("ReadVirtual handled = false")
	}
	if !strings.Contains(content, "# Body") {
		t.Fatalf("content = %q", content)
	}

	content, handled, err = store.ReadVirtual(filepath.Join(skillDir, "extra.md"))
	if err != nil {
		t.Fatalf("ReadVirtual extra error = %v", err)
	}
	if !handled {
		t.Fatal("extra not handled")
	}
	if content != "extra content" {
		t.Fatalf("extra content = %q", content)
	}
}

func TestStoreFormatInvocationLocal(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "fmt-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: fmt-skill\ndescription: Format test\n---\n# Format Test\nSome instructions"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	local, _ := LoadFromDir(dir, SourceProject)
	store := NewStore(local)
	skill := local[0]
	invocation := store.FormatInvocation(skill, "extra args")
	if !strings.Contains(invocation, "Some instructions") {
		t.Fatalf("invocation missing body: %s", invocation)
	}
	if !strings.Contains(invocation, "extra args") {
		t.Fatalf("invocation missing args: %s", invocation)
	}
	if !strings.Contains(invocation, skill.BaseDir) {
		t.Fatalf("invocation missing baseDir: %s", invocation)
	}
}
