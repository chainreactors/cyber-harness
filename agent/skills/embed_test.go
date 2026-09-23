package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureBundle is a stand-in extension contribution for store tests.
func fixtureBundle() Bundle {
	const location = "fixture://skills/fixture/SKILL.md"
	return Bundle{
		Skills: []Skill{{
			Name: "fixture", Description: "Bundle fixture", Source: SourceBundle,
			Location: location, BaseDir: "fixture://skills/fixture",
		}},
		ReadVirtual: func(uri string) (string, bool, error) {
			if uri != location {
				return "", false, nil
			}
			return "---\nname: fixture\ndescription: Bundle fixture\n---\n# Fixture\nBundle body", true, nil
		},
	}
}

func TestBundleSkillLoadsAndReads(t *testing.T) {
	store := NewStore(nil)
	if _, ok := store.ByName("fixture"); ok {
		t.Fatal("unselected bundle skill was loaded")
	}
	if _, err := store.Add(fixtureBundle()); err != nil {
		t.Fatal(err)
	}
	skill, ok := store.ByName("fixture")
	if !ok {
		t.Fatal("missing fixture")
	}
	if skill.Location != "fixture://skills/fixture/SKILL.md" {
		t.Fatalf("location = %q", skill.Location)
	}
	if body := store.ReadBody("fixture"); body != "# Fixture\nBundle body" {
		t.Fatalf("body = %q", body)
	}
}

func TestExpandCommand(t *testing.T) {
	store := NewStore(nil)
	if _, err := store.Add(fixtureBundle()); err != nil {
		t.Fatal(err)
	}

	expanded := ExpandCommand("/skill:fixture check this target", store)
	for _, want := range []string{
		`<skill name="fixture" location="fixture://skills/fixture/SKILL.md">`,
		"References are relative to fixture://skills/fixture.",
		"# Fixture",
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

func TestApplySelectedAcceptsCLIFilePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.md")
	raw := "---\nname: fixture\ndescription: A CLI fixture\n---\n# Fixture\nCLI_SKILL_OK"
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, diagnostics, ok := LoadFromFile(path)
	if !ok || len(diagnostics) != 0 {
		t.Fatalf("LoadFromFile() = %#v, %#v, %v", loaded, diagnostics, ok)
	}
	store := NewStore([]Skill{loaded})
	selected, err := store.ApplySelected("reply", []string{path})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(selected, "CLI_SKILL_OK") || !strings.HasSuffix(selected, "reply") {
		t.Fatalf("selected skill = %q", selected)
	}
}

func TestApplySelectedRejectsUnknownSkill(t *testing.T) {
	store := NewStore(nil)
	if _, err := store.ApplySelected("reply", []string{"missing"}); err == nil {
		t.Fatal("unknown skill should fail")
	}
}

func TestApplySelectedPassesIntentThrough(t *testing.T) {
	store := NewStore(nil)
	intent, err := store.ApplySelected("focus on risky exposed services", nil)
	if err != nil {
		t.Fatalf("ApplySelected() error = %v", err)
	}
	if !strings.Contains(intent, "focus on risky exposed services") {
		t.Fatalf("intent missing user text:\n%s", intent)
	}
}

func TestReadVirtual(t *testing.T) {
	store := NewStore(nil)
	if _, err := store.Add(fixtureBundle()); err != nil {
		t.Fatal(err)
	}
	content, handled, err := store.ReadVirtual("fixture://skills/fixture/SKILL.md")
	if err != nil || !handled {
		t.Fatalf("ReadVirtual() handled=%v err=%v", handled, err)
	}
	if !strings.Contains(content, "name: fixture") {
		t.Fatalf("unexpected content:\n%s", content)
	}

	// Unknown virtual URIs are not handled: no source claims them.
	if _, handled, err = store.ReadVirtual("cyber://skills/missing/SKILL.md"); handled || err != nil {
		t.Fatalf("missing handled=%v err=%v, want unhandled", handled, err)
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

func TestOverrideBundleWithLocal(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "fixture")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: fixture\ndescription: Overridden fixture skill\n---\n# Overridden\nLocal override body"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle := fixtureBundle()
	local, _ := LoadFromDir(dir, SourceProject)
	all := append(append([]Skill(nil), bundle.Skills...), local...)
	store := newStoreWithOverride(all)

	skill, ok := store.ByName("fixture")
	if !ok {
		t.Fatal("missing fixture")
	}
	if skill.Source != SourceProject {
		t.Fatalf("source = %q, want project (override)", skill.Source)
	}
	if skill.Description != "Overridden fixture skill" {
		t.Fatalf("description = %q", skill.Description)
	}
	body := store.ReadBody("fixture")
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
