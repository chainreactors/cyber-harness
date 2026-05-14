package agent

import (
	"fmt"
	"strings"

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
}

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

	if cfg.ScannerAgentMode {
		sb.WriteString(`## Scanner Agent Constraints

- Execute the scanner command provided in the task via the bash tool.
- For structured data processing, re-run the scanner with ` + "`-j`" + ` flag and use ` + "`parse_results`" + `/` + "`filter_results`" + ` pseudo-commands via bash.
- Use conservative thread counts and timeouts.
- When done, stop calling tools and provide your findings.
`)
	} else {
		sb.WriteString(`## Execution Constraints

Your bash tool is **stateless** — every command runs in a fresh ` + "`sh -c`" + ` process with a hard timeout. There is no persistent session and no environment variables carried between calls.

For long-running services (listeners, tunnels, servers), pass ` + "`background: true`" + ` — the command starts in its own process group and returns a PID immediately.

Foreground commands that block without producing output (e.g. a listener waiting for connections) will hang until timeout. Always prefer non-blocking alternatives.

Consequences for remote command execution: interactive shells, ` + "`su`" + `, interactive ` + "`python`" + `/` + "`mysql`" + ` prompts, and ` + "`expect`" + `-style dialogs do not work. Any remote execution you achieve must follow a "one command in → stdout out" pattern — each invocation self-contained.

## Data Exfiltration Priority

When you need to move data off a target, use these methods in order of preference:
1. ` + "`curl`" + `/` + "`wget`" + ` POST to your listener (single fire-and-forget command)
2. ` + "`scp`" + `/` + "`sftp`" + ` with available credentials
3. Write to a file, then retrieve with a separate command
4. Base64-encode small payloads into command output
5. Start a listener with ` + "`background: true`" + ` only when the above methods are unavailable

## Rules

- Use conservative thread counts and timeouts to avoid overwhelming targets or fragile services.
- When you have completed the task, stop calling tools and provide your findings.
`)
	}

	return sb.String()
}

func hasVisionTool(tools *command.CommandRegistry) bool {
	if tools == nil {
		return false
	}
	_, ok := tools.GetTool("vision")
	return ok
}

