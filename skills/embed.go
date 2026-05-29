package skills

import (
	"embed"
	"encoding/xml"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

const uriPrefix = "aiscan://skills/"

//go:embed all:*
var embeddedFS embed.FS

type Skill struct {
	Name        string
	Description string
	Location    string
	BaseDir     string
	Body        string
	Raw         string
	Internal    bool

	Agent           bool
	AgentMaxTurns   int
	AgentModel      string
	AgentBackground bool
}

type Diagnostic struct {
	Path    string
	Message string
}

type Store struct {
	Skills []Skill

	byName     map[string]Skill
	byLocation map[string]Skill
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
			diagnostics = append(diagnostics, Diagnostic{Path: filePath, Message: err.Error()})
			continue
		}
		skill, skillDiagnostics, ok := parseSkill(filePath, entry.Name(), string(raw))
		diagnostics = append(diagnostics, skillDiagnostics...)
		if !ok {
			continue
		}
		if !skillAvailable(skill.Name) {
			continue
		}
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

func LoadEmbeddedStore() (*Store, []Diagnostic) {
	loaded, diagnostics := LoadEmbedded()
	return NewStore(loaded), diagnostics
}

func NewStore(skills []Skill) *Store {
	store := &Store{
		Skills:     append([]Skill(nil), skills...),
		byName:     make(map[string]Skill, len(skills)),
		byLocation: make(map[string]Skill, len(skills)),
	}
	for _, skill := range skills {
		store.byName[skill.Name] = skill
		store.byLocation[skill.Location] = skill
	}
	return store
}

func (s *Store) ByName(name string) (Skill, bool) {
	if s == nil {
		return Skill{}, false
	}
	skill, ok := s.byName[name]
	return skill, ok
}

func (s *Store) AgentTypes() []Skill {
	if s == nil {
		return nil
	}
	var agents []Skill
	for _, skill := range s.Skills {
		if skill.Agent {
			agents = append(agents, skill)
		}
	}
	return agents
}

func (s *Store) ByLocation(location string) (Skill, bool) {
	if s == nil {
		return Skill{}, false
	}
	skill, ok := s.byLocation[location]
	return skill, ok
}

func (s *Store) ReadVirtual(location string) (string, bool, error) {
	if strings.HasPrefix(location, uriPrefix) {
		skill, ok := s.ByLocation(location)
		if !ok {
			return "", true, fmt.Errorf("virtual file not found: %s", location)
		}
		return skill.Raw, true, nil
	}

	// Support relative paths: "skills/verify/example.md" or "verify/SKILL.md"
	embedPath := normalizeEmbedPath(location)
	if embedPath == "" {
		return "", false, nil
	}
	if name := skillNameFromEmbedPath(embedPath); name != "" && !skillAvailable(name) {
		return "", true, fmt.Errorf("virtual file not available in this build: %s", location)
	}
	data, err := fs.ReadFile(embeddedFS, embedPath)
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

func (s *Store) GlobVirtual(pattern string) ([]string, bool) {
	embedPattern := normalizeEmbedPath(pattern)
	if embedPattern == "" {
		return nil, false
	}
	matches, err := fs.Glob(embeddedFS, embedPattern)
	if err != nil || len(matches) == 0 {
		return nil, false
	}
	results := make([]string, 0, len(matches))
	for _, m := range matches {
		if name := skillNameFromEmbedPath(m); name != "" && !skillAvailable(name) {
			continue
		}
		results = append(results, "skills/"+m)
	}
	if len(results) == 0 {
		return nil, false
	}
	return results, true
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
	// Direct subpath like "verify/SKILL.md"
	if !strings.HasPrefix(location, "/") && !strings.HasPrefix(location, ".") {
		return location
	}
	return ""
}

func skillNameFromEmbedPath(embedPath string) string {
	embedPath = path.Clean(strings.TrimSpace(embedPath))
	if embedPath == "." || strings.HasPrefix(embedPath, "..") {
		return ""
	}
	name, _, _ := strings.Cut(embedPath, "/")
	return name
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

func FormatInvocation(skill Skill, args string) string {
	var sb strings.Builder
	sb.WriteString(`<skill name="`)
	sb.WriteString(skill.Name)
	sb.WriteString(`" location="`)
	sb.WriteString(skill.Location)
	sb.WriteString(`">` + "\n")
	sb.WriteString("References are relative to ")
	sb.WriteString(skill.BaseDir)
	sb.WriteString(".\n\n")
	sb.WriteString(strings.TrimSpace(skill.Body))
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

	return FormatInvocation(skill, args)
}

func parseSkill(filePath, defaultName, raw string) (Skill, []Diagnostic, bool) {
	frontmatter, body := splitFrontmatter(raw)
	name := strings.TrimSpace(frontmatter["name"])
	if name == "" {
		name = defaultName
	}
	description := strings.TrimSpace(frontmatter["description"])
	var diagnostics []Diagnostic
	if description == "" {
		diagnostics = append(diagnostics, Diagnostic{Path: filePath, Message: "description is required"})
		return Skill{}, diagnostics, false
	}
	internal := strings.EqualFold(strings.TrimSpace(frontmatter["internal"]), "true")
	isAgent := strings.EqualFold(strings.TrimSpace(frontmatter["agent"]), "true")
	agentBackground := strings.EqualFold(strings.TrimSpace(frontmatter["agent_background"]), "true")
	agentMaxTurns := 0
	if v := strings.TrimSpace(frontmatter["agent_max_turns"]); v != "" {
		_, _ = fmt.Sscanf(v, "%d", &agentMaxTurns)
	}
	agentModel := strings.TrimSpace(frontmatter["agent_model"])

	location := uriPrefix + name + "/SKILL.md"
	return Skill{
		Name:            name,
		Description:     description,
		Location:        location,
		BaseDir:         uriPrefix + name,
		Body:            strings.TrimSpace(body),
		Raw:             raw,
		Internal:        internal,
		Agent:           isAgent,
		AgentMaxTurns:   agentMaxTurns,
		AgentModel:      agentModel,
		AgentBackground: agentBackground,
	}, diagnostics, true
}

func splitFrontmatter(raw string) (map[string]string, string) {
	frontmatter := make(map[string]string)
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return frontmatter, raw
	}
	end := strings.Index(normalized[4:], "\n---")
	if end < 0 {
		return frontmatter, raw
	}
	header := normalized[4 : 4+end]
	body := normalized[4+end:]
	body = strings.TrimPrefix(body, "\n---")
	body = strings.TrimPrefix(body, "---")
	body = strings.TrimPrefix(body, "\n")
	for _, line := range strings.Split(header, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key != "" {
			frontmatter[key] = value
		}
	}
	return frontmatter, body
}
