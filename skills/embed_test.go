package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var baseExpectedSkills = []string{"aiscan", "playwright", "scan", "gogo", "spray", "zombie", "neutron", "proton"}
var baseInternalSkills = []string{"playwright", "scan", "gogo", "spray", "zombie", "neutron", "proton"}

var extraExpected []string
var extraInternal []string

//nolint:unused // called from build-tagged test files
func addExpectedSkill(name string, internal bool) {
	extraExpected = append(extraExpected, name)
	if internal {
		extraInternal = append(extraInternal, name)
	}
}

func expectedEmbeddedSkillNames() []string {
	return append(append([]string(nil), baseExpectedSkills...), extraExpected...)
}

func internalPromptSkillNames() []string {
	return append(append([]string(nil), baseInternalSkills...), extraInternal...)
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
	body := ReadBody("aiscan")
	if body == "" {
		t.Fatal("ReadBody returned empty")
	}
	if strings.Contains(body, "---") {
		t.Fatalf("ReadBody contains frontmatter: %q", body)
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
	for _, internal := range internalPromptSkillNames() {
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
	if !strings.Contains(content, "name: aiscan") || !strings.Contains(content, "# Aiscan") {
		t.Fatalf("unexpected content:\n%s", content)
	}

	_, handled, err = store.ReadVirtual("aiscan://skills/missing/SKILL.md")
	if !handled || err == nil {
		t.Fatalf("missing handled=%v err=%v, want handled error", handled, err)
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

func TestSplitFrontmatterBackwardCompat(t *testing.T) {
	raw := "---\nname: test\ndescription: desc\ninternal: true\n---\nBody"
	m, body := SplitFrontmatter(raw)
	if m["name"] != "test" {
		t.Fatalf("name = %q", m["name"])
	}
	if m["description"] != "desc" {
		t.Fatalf("description = %q", m["description"])
	}
	if !strings.Contains(body, "Body") {
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
	skillDir := filepath.Join(dir, "aiscan")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: aiscan\ndescription: Overridden aiscan skill\n---\n# Overridden\nLocal override body"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	embedded, _ := LoadEmbedded()
	local, _ := LoadFromDir(dir, SourceProject)
	all := append(embedded, local...)
	store := newStoreWithOverride(all)

	skill, ok := store.ByName("aiscan")
	if !ok {
		t.Fatal("missing aiscan")
	}
	if skill.Source != SourceProject {
		t.Fatalf("source = %q, want project (override)", skill.Source)
	}
	if skill.Description != "Overridden aiscan skill" {
		t.Fatalf("description = %q", skill.Description)
	}
	body := store.ReadBody("aiscan")
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
