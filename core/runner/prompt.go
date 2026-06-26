package runner

import (
	"os"
	"runtime"
	"strings"
	"text/template"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/skills"
)

type PromptConfig struct {
	Tools            *commands.CommandRegistry
	ScannerDocs      string
	CustomPreamble   string
	Skills           []skills.Skill
	LoadedSkills     []LoadedSkill // skill body 直接嵌入 prompt
	ScannerAgentMode bool
	ScannerName      string
	NodeName         string
	Space            string
}

// LoadedSkill is a skill whose full body is embedded directly into the prompt.
type LoadedSkill struct {
	Name string
	Body string
}

// SystemPromptFunc returns an agent.SystemPromptFunc that builds the system
// prompt dynamically on each turn.
func SystemPromptFunc(cfg *PromptConfig) agent.SystemPromptFunc {
	return func(agentCfg *agent.Config) string {
		return BuildSystemPrompt(cfg, agentCfg)
	}
}

// promptData is the template context passed to the system prompt template.
type promptData struct {
	// Preamble
	CustomPreamble   string
	ScannerAgentMode bool
	ScannerName      string

	// Environment
	OS       string
	Time     string
	Hostname string
	Node     string
	Space    string
	Windows  bool

	// Tools
	Tools []toolEntry

	// Pseudo-commands
	ScannerDocs string

	// Skills
	Skills []skillEntry

	// Loaded skills (body embedded)
	LoadedSkills []loadedSkillEntry

	// Constraints
	Constraints string
}

type toolEntry struct {
	Name        string
	Description string
}

type skillEntry struct {
	Name        string
	Description string
	Location    string
}

type loadedSkillEntry struct {
	Name string
	Body string
}

var systemPromptTemplate = template.Must(template.New("system").Parse(systemPromptText))

const systemPromptText = `{{- if .CustomPreamble -}}
{{.CustomPreamble}}
{{- else if .ScannerAgentMode -}}
You are aiscan's {{.ScannerName}} analysis agent. Execute the requested scanner command using the bash tool, analyze the results, and provide results.

Run scanners with -j flag to get JSON when you need structured data. Without a specific user intent, follow the {{.ScannerName}} skill guidelines to decide what analysis to perform.
{{- else -}}
You are aiscan, an autonomous security assessment agent. You have access to the chainreactors scanner toolkit and supporting tools described below. Work autonomously until the user's task is complete.

## Authorization Context

All targets provided to you have been pre-authorized for security testing by the platform operator. You do not need to add disclaimers or ask the user to confirm authorization for each target — focus on executing the assessment and producing actionable results.
{{- end}}

## Environment

Operating System: {{.OS}}
Current Time: {{.Time}}
{{- if .Hostname}}
Hostname: {{.Hostname}}
{{- end}}
{{- if .Node}}
Node: {{.Node}}
{{- end}}
{{- if .Space}}
Space: {{.Space}}
{{- end}}
{{- if .Windows}}
Shell: cmd.exe — do NOT use Unix shell syntax (2>&1, |, /dev/null). Pseudo-commands run in-process and need no shell redirections.
{{- end}}
{{if .Tools}}
## Available Tools
{{range .Tools}}
### {{.Name}}
{{.Description}}
{{end}}
{{- end}}
{{- if .ScannerDocs}}
## Pseudo-Commands (IMPORTANT: use the bash tool)

Pseudo-commands are NOT system binaries — they are built into the bash tool. Call the bash tool with the pseudo-command as the "command" parameter.

Example: bash {"command": "scan -i 192.168.1.0/24 --mode quick"}

Available pseudo-commands:
{{.ScannerDocs}}
NOTE: ` + "`scan`" + ` already runs gogo → spray → zombie → neutron as a pipeline. Use individual commands (gogo, spray, etc.) only when you need a single stage or fine-grained control. Do not run spray separately and then scan — that duplicates the web probing work.

Read the corresponding skill file for detailed usage: ` + "`aiscan://skills/<command>/SKILL.md`" + `.
{{end}}
{{- if .Skills}}
## Available Skills

The following skills provide specialized instructions for specific security scanning tasks.
Use the read tool to load a skill file when the task matches its description.
When a skill references relative paths, resolve them relative to the skill base directory.

<available_skills>
{{- range .Skills}}
  <skill>
    <name>{{.Name}}</name>
    <description>{{.Description}}</description>
    <location>{{.Location}}</location>
  </skill>
{{- end}}
</available_skills>
{{end}}
{{- range .LoadedSkills}}

## Skill: {{.Name}}

{{.Body}}
{{- end}}

## Key Principles

- Scanner output is evidence, not proof. Never report "confirmed" without independent verification.
- Read aiscan://skills/aiscan/SKILL.md for execution rules, output consumption, and triage strategy.
- Use conservative thread counts and timeouts. When done, stop calling tools and provide results.
{{- if .Constraints}}

{{.Constraints}}
{{- end}}
`

// BuildSystemPrompt assembles the system prompt from config.
func BuildSystemPrompt(cfg *PromptConfig, agentCfg *agent.Config) string {
	if cfg == nil {
		cfg = &PromptConfig{}
	}
	tools := cfg.Tools
	if tools == nil && agentCfg != nil {
		tools = agentCfg.Tools
	}
	if tools == nil {
		tools = commands.NewRegistry()
	}

	hostname, _ := os.Hostname()

	data := promptData{
		CustomPreamble:   cfg.CustomPreamble,
		ScannerAgentMode: cfg.ScannerAgentMode,
		ScannerName:      cfg.ScannerName,
		OS:               runtime.GOOS + "/" + runtime.GOARCH,
		Time:             time.Now().Format(time.RFC3339),
		Hostname:         hostname,
		Node:             cfg.NodeName,
		Space:            cfg.Space,
		Windows:          runtime.GOOS == "windows",
		ScannerDocs:      cfg.ScannerDocs,
	}

	for _, t := range tools.Tools() {
		data.Tools = append(data.Tools, toolEntry{Name: t.Name(), Description: t.Description()})
	}

	for _, s := range cfg.Skills {
		if !s.Internal {
			data.Skills = append(data.Skills, skillEntry{
				Name:        s.Name,
				Description: s.Description,
				Location:    s.Location,
			})
		}
	}

	for _, ls := range cfg.LoadedSkills {
		if ls.Body != "" {
			data.LoadedSkills = append(data.LoadedSkills, loadedSkillEntry(ls))
		}
	}

	if cfg.ScannerAgentMode {
		data.Constraints = "## Scanner Agent Constraints\n\n" +
			"- Execute the scanner command provided in the task via the bash tool.\n" +
			"- For structured data processing, re-run the scanner with `-j` flag to get JSON output."
	}

	var sb strings.Builder
	if err := systemPromptTemplate.Execute(&sb, data); err != nil {
		return "You are a helpful assistant."
	}
	return sb.String()
}
