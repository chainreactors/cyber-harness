package skills

import (
	"context"
	"embed"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/chainreactors/cyber/core/resource"
)

const uriPrefix = "cyber://skills/"

//go:embed all:*
var embeddedFS embed.FS

type SkillSource string

const (
	SourceEmbedded SkillSource = "embedded"
	SourceProject  SkillSource = "project" // .cyber/skills/
	SourceAgent    SkillSource = "agent"   // .agent/skills/
	SourceCLI      SkillSource = "cli"     // -s path
	SourceBundle   SkillSource = "bundle"  // explicitly selected extension
)

type Frontmatter struct {
	Name            string `yaml:"name"`
	Description     string `yaml:"description"`
	Internal        bool   `yaml:"internal"`
	Agent           bool   `yaml:"agent"`
	AgentMaxTurns   int    `yaml:"agent_max_turns"`
	AgentModel      string `yaml:"agent_model"`
	AgentBackground bool   `yaml:"agent_background"`
}

type Skill struct {
	Name        string
	Description string
	Location    string
	BaseDir     string
	Internal    bool
	Source      SkillSource

	Agent           bool
	AgentMaxTurns   int
	AgentModel      string
	AgentBackground bool
}

type Diagnostic struct {
	Path    string
	Message string
}

// Bundle is a static skill contribution selected by the composition root.
// Readers return handled=false for locations outside their namespace.
type Bundle struct {
	Skills      []Skill
	ReadVirtual func(string) (string, bool, error)
}

type Store struct {
	mu          sync.RWMutex
	base        []Skill
	diagnostics []Diagnostic
	batches     []*bundleBatch
	snapshot    storeSnapshot
}

type storeSnapshot struct {
	Bundles []Bundle
	Skills  []Skill
	ByName  map[string]Skill
}

type bundleBatch struct {
	bundles []Bundle
	store   *Store
	closed  bool
	mu      sync.Mutex
}

// LoadAll loads skills from all sources with override support.
// Priority (later overrides earlier): embedded < .cyber/skills/ < .agent/skills/ < CLI paths.
func LoadAll(cliPaths []string, bundles ...Bundle) (*Store, []Diagnostic) {
	directory, _ := os.Getwd()
	return LoadFrom(directory, cliPaths, bundles...)
}

// LoadFrom resolves project and relative CLI paths against the profile directory.
func LoadFrom(directory string, cliPaths []string, bundles ...Bundle) (*Store, []Diagnostic) {
	var allSkills []Skill
	var allDiags []Diagnostic

	embedded, diags := LoadEmbedded()
	allSkills = append(allSkills, embedded...)
	allDiags = append(allDiags, diags...)

	for _, rel := range []struct {
		dir    string
		source SkillSource
	}{
		{".cyber/skills", SourceProject},
		{".agent/skills", SourceAgent},
	} {
		dir := filepath.Join(directory, rel.dir)
		if directory == "" {
			continue
		}
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		local, diags := LoadFromDir(dir, rel.source)
		allSkills = append(allSkills, local...)
		allDiags = append(allDiags, diags...)
	}

	for _, p := range cliPaths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(directory, p)
		}
		info, err := os.Stat(p)
		if err != nil {
			allDiags = append(allDiags, Diagnostic{Path: p, Message: err.Error()})
			continue
		}
		if info.IsDir() {
			local, diags := LoadFromDir(p, SourceCLI)
			allSkills = append(allSkills, local...)
			allDiags = append(allDiags, diags...)
		} else {
			skill, diags, ok := LoadFromFile(p)
			allDiags = append(allDiags, diags...)
			if ok {
				allSkills = append(allSkills, skill)
			}
		}
	}

	store := NewStore(allSkills)
	store.diagnostics = append([]Diagnostic(nil), allDiags...)
	if len(bundles) > 0 {
		_, _ = store.Add(bundles...)
	}
	return store, allDiags
}

func LoadEmbedded() ([]Skill, []Diagnostic) {
	entries, err := fs.ReadDir(embeddedFS, ".")
	if err != nil {
		return nil, []Diagnostic{{Message: fmt.Sprintf("read embedded skills: %s", err.Error())}}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var loaded []Skill
	var diagnostics []Diagnostic
	seen := make(map[string]Skill)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		filePath := path.Join(entry.Name(), "SKILL.md")
		raw, err := embeddedFS.ReadFile(filePath)
		if err != nil {
			// Directories without a SKILL.md (e.g. scan/ workflow docs) are
			// plain embedded content, not skills.
			if !errors.Is(err, fs.ErrNotExist) {
				diagnostics = append(diagnostics, Diagnostic{Path: filePath, Message: err.Error()})
			}
			continue
		}
		skill, skillDiagnostics, ok := parseSkill(filePath, entry.Name(), string(raw), SourceEmbedded)
		diagnostics = append(diagnostics, skillDiagnostics...)
		if !ok {
			continue
		}
		skill.Location = uriPrefix + skill.Name + "/SKILL.md"
		skill.BaseDir = uriPrefix + skill.Name
		if existing, exists := seen[skill.Name]; exists {
			diagnostics = append(diagnostics, Diagnostic{
				Path:    filePath,
				Message: fmt.Sprintf("name %q collision with %s", skill.Name, existing.Location),
			})
			continue
		}
		seen[skill.Name] = skill
		loaded = append(loaded, skill)
	}
	return loaded, diagnostics
}

// LoadFromDir loads skills from a local directory. Each subdirectory with a SKILL.md is a skill.
func LoadFromDir(dirPath string, source SkillSource) ([]Skill, []Diagnostic) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, []Diagnostic{{Path: dirPath, Message: err.Error()}}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var loaded []Skill
	var diagnostics []Diagnostic
	seen := make(map[string]Skill)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		filePath := filepath.Join(dirPath, entry.Name(), "SKILL.md")
		raw, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		skill, skillDiags, ok := parseSkill(filePath, entry.Name(), string(raw), source)
		diagnostics = append(diagnostics, skillDiags...)
		if !ok {
			continue
		}
		skill.Location = filePath
		skill.BaseDir = filepath.Join(dirPath, entry.Name())
		if existing, exists := seen[skill.Name]; exists {
			diagnostics = append(diagnostics, Diagnostic{
				Path:    filePath,
				Message: fmt.Sprintf("name %q collision with %s", skill.Name, existing.Location),
			})
			continue
		}
		seen[skill.Name] = skill
		loaded = append(loaded, skill)
	}
	return loaded, diagnostics
}

// LoadFromFile loads a single skill from a markdown file path.
func LoadFromFile(filePath string) (Skill, []Diagnostic, bool) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return Skill{}, []Diagnostic{{Path: filePath, Message: err.Error()}}, false
	}
	raw, err := os.ReadFile(absPath)
	if err != nil {
		return Skill{}, []Diagnostic{{Path: absPath, Message: err.Error()}}, false
	}
	base := filepath.Base(absPath)
	defaultName := strings.TrimSuffix(base, filepath.Ext(base))
	skill, diags, ok := parseSkill(absPath, defaultName, string(raw), SourceCLI)
	if !ok {
		return Skill{}, diags, false
	}
	skill.Location = absPath
	skill.BaseDir = filepath.Dir(absPath)
	return skill, diags, true
}

func LoadEmbeddedStore() (*Store, []Diagnostic) {
	loaded, diagnostics := LoadEmbedded()
	return NewStore(loaded), diagnostics
}

func NewStore(skills []Skill) *Store {
	store := &Store{base: append([]Skill(nil), skills...)}
	store.rebuildLocked()
	return store
}

// newStoreWithOverride builds a store where later skills override earlier ones by name.
func newStoreWithOverride(skills []Skill) *Store {
	byName := make(map[string]Skill, len(skills))
	for _, s := range skills {
		byName[s.Name] = s
	}
	var deduped []Skill
	seen := make(map[string]bool, len(byName))
	for _, s := range skills {
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		deduped = append(deduped, byName[s.Name])
	}
	return &Store{base: append([]Skill(nil), deduped...), snapshot: storeSnapshot{Skills: deduped, ByName: byName}}
}

func (s *Store) Add(bundles ...Bundle) (resource.Handle, error) {
	if s == nil || len(bundles) == 0 {
		return nil, resource.ErrInvalid
	}
	batch := &bundleBatch{store: s, bundles: append([]Bundle(nil), bundles...)}
	s.mu.Lock()
	s.batches = append(s.batches, batch)
	s.rebuildLocked()
	s.mu.Unlock()
	return batch, nil
}

func (b *bundleBatch) Close(context.Context) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.store.mu.Lock()
	for i, candidate := range b.store.batches {
		if candidate == b {
			b.store.batches = append(b.store.batches[:i], b.store.batches[i+1:]...)
			break
		}
	}
	b.store.rebuildLocked()
	b.store.mu.Unlock()
	b.closed = true
	return nil
}

func (s *Store) rebuildLocked() {
	var all []Skill
	for _, skill := range s.base {
		if skill.Source == SourceEmbedded {
			all = append(all, skill)
		}
	}
	var bundles []Bundle
	for _, batch := range s.batches {
		bundles = append(bundles, batch.bundles...)
		for _, bundle := range batch.bundles {
			all = append(all, bundle.Skills...)
		}
	}
	for _, skill := range s.base {
		if skill.Source != SourceEmbedded {
			all = append(all, skill)
		}
	}
	resolved := newStoreWithOverride(all).snapshot
	resolved.Bundles = bundles
	s.snapshot = resolved
}

func (s *Store) All() []Skill {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Skill(nil), s.snapshot.Skills...)
}

func (s *Store) Diagnostics() []Diagnostic {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Diagnostic(nil), s.diagnostics...)
}

func (s *Store) Replace(skills []Skill, diagnostics []Diagnostic) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.base = append(s.base[:0], skills...)
	s.diagnostics = append(s.diagnostics[:0], diagnostics...)
	s.rebuildLocked()
	s.mu.Unlock()
}

func (s *Store) ByName(name string) (Skill, bool) {
	if s == nil {
		return Skill{}, false
	}
	s.mu.RLock()
	skill, ok := s.snapshot.ByName[name]
	s.mu.RUnlock()
	return skill, ok
}

func (s *Store) AgentTypes() []Skill {
	if s == nil {
		return nil
	}
	var agents []Skill
	for _, skill := range s.All() {
		if skill.Agent {
			agents = append(agents, skill)
		}
	}
	return agents
}

// ReadVirtual reads a file from skill sources (embedded or local).
func (s *Store) ReadVirtual(location string) (string, bool, error) {
	s.mu.RLock()
	bundles := append([]Bundle(nil), s.snapshot.Bundles...)
	s.mu.RUnlock()
	for _, bundle := range bundles {
		if bundle.ReadVirtual != nil {
			if raw, handled, err := bundle.ReadVirtual(location); handled || err != nil {
				return raw, handled, err
			}
		}
	}
	if filepath.IsAbs(location) {
		if !s.isKnownLocalPath(location) {
			return "", false, nil
		}
		data, err := os.ReadFile(location)
		if err != nil {
			return "", true, fmt.Errorf("local skill file not found: %s", location)
		}
		return string(data), true, nil
	}

	var embedPath string
	if strings.HasPrefix(location, uriPrefix) {
		embedPath = strings.TrimPrefix(location, uriPrefix)
	} else {
		embedPath = normalizeEmbedPath(location)
		if embedPath == "" {
			return "", false, nil
		}
	}
	data, err := fs.ReadFile(embeddedFS, embedPath)
	if err != nil {
		return "", true, fmt.Errorf("virtual file not found: %s", location)
	}
	return string(data), true, nil
}

func (s *Store) isKnownLocalPath(absPath string) bool {
	for _, skill := range s.All() {
		if skill.Source == SourceEmbedded || skill.Source == "" {
			continue
		}
		if strings.HasPrefix(absPath, skill.BaseDir+string(filepath.Separator)) || absPath == skill.Location {
			return true
		}
	}
	return false
}

func (s *Store) GlobVirtual(pattern string) ([]string, bool) {
	var allMatches []string

	embedPattern := normalizeEmbedPath(pattern)
	if embedPattern != "" {
		matches, err := fs.Glob(embeddedFS, embedPattern)
		if err == nil {
			for _, m := range matches {
				allMatches = append(allMatches, "skills/"+m)
			}
		}
	}

	for _, skill := range s.All() {
		if skill.Source == SourceEmbedded || skill.Source == "" {
			continue
		}
		localPattern := filepath.Join(skill.BaseDir, filepath.Base(pattern))
		matches, err := filepath.Glob(localPattern)
		if err == nil {
			allMatches = append(allMatches, matches...)
		}
	}

	if len(allMatches) == 0 {
		return nil, false
	}
	return allMatches, true
}

// ReadBody reads a skill's markdown body (without frontmatter).
// It checks the store for local overrides before falling back to embedded.
func (s *Store) ReadBody(name string) string {
	if s == nil {
		return readEmbeddedBody(name)
	}
	skill, ok := s.ByName(name)
	if !ok {
		return readEmbeddedBody(name)
	}
	if skill.Source == SourceBundle {
		raw, handled, err := s.ReadVirtual(skill.Location)
		if err != nil || !handled {
			return ""
		}
		_, body := splitRaw(raw)
		return strings.TrimSpace(body)
	}
	if skill.Source == SourceEmbedded || skill.Source == "" {
		return readEmbeddedBody(name)
	}
	raw, err := os.ReadFile(skill.Location)
	if err != nil {
		return ""
	}
	_, body := splitRaw(string(raw))
	return strings.TrimSpace(body)
}

func readEmbeddedBody(name string) string {
	filePath := path.Join(name, "SKILL.md")
	raw, err := embeddedFS.ReadFile(filePath)
	if err != nil {
		return ""
	}
	_, body := splitRaw(string(raw))
	return strings.TrimSpace(body)
}

// ReadFile reads any file from the embedded skills filesystem.
func ReadFile(embedPath string) string {
	normalized := normalizeEmbedPath(embedPath)
	if normalized == "" {
		return ""
	}
	data, err := fs.ReadFile(embeddedFS, normalized)
	if err != nil {
		return ""
	}
	return string(data)
}

func normalizeEmbedPath(location string) string {
	location = strings.TrimSpace(location)
	if location == "" {
		return ""
	}
	location = path.Clean(location)
	if strings.HasPrefix(location, "skills/") {
		return strings.TrimPrefix(location, "skills/")
	}
	if !strings.HasPrefix(location, "/") && !strings.HasPrefix(location, ".") {
		return location
	}
	return ""
}

// ReadVirtualBody reads a virtual file and returns its body with frontmatter stripped.
func (s *Store) ReadVirtualBody(location string) (string, bool, error) {
	raw, ok, err := s.ReadVirtual(location)
	if !ok || err != nil {
		return "", ok, err
	}
	_, body := splitRaw(raw)
	return strings.TrimSpace(body), true, nil
}

func FormatForPrompt(skills []Skill) string {
	visible := make([]Skill, 0, len(skills))
	for _, skill := range skills {
		if !skill.Internal {
			visible = append(visible, skill)
		}
	}
	if len(visible) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n## Available Skills\n\n")
	sb.WriteString("The following skills provide specialized instructions for specific security scanning tasks.\n")
	sb.WriteString("Use the read tool to load a skill file when the task matches its description.\n")
	sb.WriteString("When a skill references relative paths, resolve them relative to the skill base directory.\n\n")
	sb.WriteString("<available_skills>\n")
	for _, skill := range visible {
		sb.WriteString("  <skill>\n")
		sb.WriteString("    <name>")
		appendEscapedXML(&sb, skill.Name)
		sb.WriteString("</name>\n")
		sb.WriteString("    <description>")
		appendEscapedXML(&sb, skill.Description)
		sb.WriteString("</description>\n")
		sb.WriteString("    <location>")
		appendEscapedXML(&sb, skill.Location)
		sb.WriteString("</location>\n")
		sb.WriteString("  </skill>\n")
	}
	sb.WriteString("</available_skills>\n")
	return sb.String()
}

func appendEscapedXML(sb *strings.Builder, value string) {
	_ = xml.EscapeText(sb, []byte(value))
}

// FormatInvocation formats a skill invocation with its body.
// It uses the store to resolve local skill bodies.
func (s *Store) FormatInvocation(skill Skill, args string) string {
	body := s.ReadBody(skill.Name)
	return formatInvocationBody(skill, body, args)
}

// FormatVirtualInvocation formats an embedded virtual document (e.g. an OKF
// tool concept) for prompt injection with the same wrapper as skill invocations.
func FormatVirtualInvocation(name, location, body string) string {
	return formatInvocationBody(Skill{Name: name, Location: location, BaseDir: path.Dir(location)}, body, "")
}

func formatInvocationBody(skill Skill, body string, args string) string {
	var sb strings.Builder
	sb.WriteString(`<skill name="`)
	sb.WriteString(skill.Name)
	sb.WriteString(`" location="`)
	sb.WriteString(skill.Location)
	sb.WriteString(`">` + "\n")
	sb.WriteString("References are relative to ")
	sb.WriteString(skill.BaseDir)
	sb.WriteString(".\n\n")
	sb.WriteString(body)
	sb.WriteString("\n</skill>")
	if strings.TrimSpace(args) != "" {
		sb.WriteString("\n\n")
		sb.WriteString(strings.TrimSpace(args))
	}
	return sb.String()
}

func ExpandCommand(text string, store *Store) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/skill:") {
		return text
	}
	rest := strings.TrimPrefix(trimmed, "/skill:")
	if rest == "" {
		return text
	}
	name, args, _ := strings.Cut(rest, " ")
	name = strings.TrimSpace(name)
	args = strings.TrimSpace(args)
	if name == "" {
		return text
	}
	skill, ok := store.ByName(name)
	if !ok {
		return text
	}

	return store.FormatInvocation(skill, args)
}

func parseSkill(filePath, defaultName, raw string, source SkillSource) (Skill, []Diagnostic, bool) {
	fm, _ := ParseFrontmatter(raw)
	name := strings.TrimSpace(fm.Name)
	if name == "" {
		name = defaultName
	}
	description := strings.TrimSpace(fm.Description)
	var diagnostics []Diagnostic
	if description == "" {
		diagnostics = append(diagnostics, Diagnostic{Path: filePath, Message: "description is required"})
		return Skill{}, diagnostics, false
	}

	return Skill{
		Name:            name,
		Description:     description,
		Internal:        fm.Internal,
		Source:          source,
		Agent:           fm.Agent,
		AgentMaxTurns:   fm.AgentMaxTurns,
		AgentModel:      fm.AgentModel,
		AgentBackground: fm.AgentBackground,
	}, diagnostics, true
}

// ParseFrontmatter parses YAML frontmatter into a typed struct and returns the body.
func ParseFrontmatter(raw string) (Frontmatter, string) {
	yamlBlock, body := splitRaw(raw)
	if yamlBlock == "" {
		return Frontmatter{}, body
	}
	var fm Frontmatter
	_ = yaml.Unmarshal([]byte(yamlBlock), &fm)
	return fm, body
}

func splitRaw(raw string) (yamlBlock string, body string) {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return "", raw
	}
	end := strings.Index(normalized[4:], "\n---")
	if end < 0 {
		return "", raw
	}
	yamlBlock = normalized[4 : 4+end]
	body = normalized[4+end:]
	body = strings.TrimPrefix(body, "\n---")
	body = strings.TrimPrefix(body, "---")
	body = strings.TrimPrefix(body, "\n")
	return yamlBlock, body
}
