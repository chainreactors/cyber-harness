package agent

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/skills"
)

type PromptConfig struct {
	Tools            *command.CommandRegistry
	ScannerDocs      string
	CustomPreamble   string
	Skills           []skills.Skill
	ScannerAgentMode bool
	ScannerName      string
	NodeName         string
	Space            string
}

const sharedKeyPrinciples = `## Key Principles

- Scanner output is evidence, not proof. Never report "confirmed" without independent verification.
- Read aiscan://skills/aiscan/SKILL.md for execution rules, output consumption, and triage strategy.
- Read aiscan://skills/verify/SKILL.md before reporting any vulnerability finding.
- Use conservative thread counts and timeouts. When done, stop calling tools and provide findings.
`

func BuildSystemPrompt(cfg *PromptConfig) string {
	if cfg == nil {
		cfg = &PromptConfig{}
	}
	tools := cfg.Tools
	if tools == nil {
		tools = command.NewRegistry()
	}

	var sb strings.Builder

	if cfg.CustomPreamble != "" {
		sb.WriteString(cfg.CustomPreamble)
		sb.WriteString("\n\n")
	} else if cfg.ScannerAgentMode {
		sb.WriteString(fmt.Sprintf(`You are aiscan's %s analysis agent. Execute the requested scanner command using the bash tool, analyze the results, and provide findings.

You can use parse_results and filter_results pseudo-commands via bash for structured analysis of JSON scanner output — run scanners with -j flag to get JSON when you need structured data. Without a specific user intent, follow the %s skill guidelines to decide what analysis to perform.

`, cfg.ScannerName, cfg.ScannerName))
	} else {
		sb.WriteString(`You are aiscan, an autonomous security assessment agent. You have access to the chainreactors scanner toolkit and supporting tools described below. Work autonomously until the user's task is complete.

`)
	}

	sb.WriteString("## Environment\n\n")
	sb.WriteString(fmt.Sprintf("Operating System: %s/%s\n", runtime.GOOS, runtime.GOARCH))
	sb.WriteString(fmt.Sprintf("Current Time: %s\n", time.Now().Format(time.RFC3339)))
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		sb.WriteString(fmt.Sprintf("Hostname: %s\n", hostname))
	}
	if cfg.NodeName != "" {
		sb.WriteString(fmt.Sprintf("Node: %s\n", cfg.NodeName))
	}
	if cfg.Space != "" {
		sb.WriteString(fmt.Sprintf("Space: %s\n", cfg.Space))
	}
	if runtime.GOOS == "windows" {
		sb.WriteString("Shell: cmd.exe — do NOT use Unix shell syntax (2>&1, |, /dev/null). Pseudo-commands run in-process and need no shell redirections.\n")
	}
	sb.WriteString("\n")

	sb.WriteString("## Available Tools\n\n")
	for _, t := range tools.Tools() {
		sb.WriteString(fmt.Sprintf("### %s\n%s\n\n", t.Name(), t.Description()))
	}

	if cfg.ScannerDocs != "" {
		sb.WriteString("## Pseudo-Commands (IMPORTANT: use the bash tool)\n\n")
		sb.WriteString(`Pseudo-commands are NOT system binaries — they are built into the bash tool.

**How to use them:** Call the bash tool and put the pseudo-command as the "command" parameter. The bash tool will intercept and execute it internally.

**Correct example:**
Tool call: bash
Arguments: {"command": "scan -i 192.168.1.0/24 --mode quick"}

**WRONG (do NOT do these):**
- Do NOT call pseudo-commands as standalone tools — they do not exist as separate tools.
- Do NOT run them as shell commands — they are not installed on the system.

Available pseudo-commands and their flags:

`)
		sb.WriteString(cfg.ScannerDocs)
		sb.WriteString("\n\n")
	}

	if skillPrompt := skills.FormatForPrompt(cfg.Skills); skillPrompt != "" {
		sb.WriteString(skillPrompt)
		sb.WriteString("\n\n")
	}

	if hasVisionTool(tools) {
		sb.WriteString(`## Vision Analysis

The vision tool requires a local file path. If you need to analyze a remote image, download it first, then pass the local path to vision.

`)
	}

	sb.WriteString(sharedKeyPrinciples)

	if cfg.ScannerAgentMode {
		sb.WriteString(`## Scanner Agent Constraints

- Execute the scanner command provided in the task via the bash tool.
- For structured data processing, re-run the scanner with ` + "`-j`" + ` flag and use ` + "`parse_results`" + `/` + "`filter_results`" + ` pseudo-commands via bash.

`)
	}

	return sb.String()
}

func hasVisionTool(tools *command.CommandRegistry) bool {
	if tools == nil {
		return false
	}
	if tools.Has("vision") {
		return true
	}
	_, ok := tools.GetTool("vision")
	return ok
}
