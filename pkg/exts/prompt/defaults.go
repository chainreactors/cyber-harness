package prompt

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"

	agentprompt "github.com/chainreactors/cyber/agent/prompt"
)

func defaultContributions() []agentprompt.Contribution {
	return []agentprompt.Contribution{
		{
			Name:    "prompt.defaults.agent",
			Targets: []agentprompt.Target{agentprompt.MainSystem},
			Apply:   buildAgentPrompt,
		},
		{
			Name:    "prompt.defaults.evaluator.system",
			Targets: []agentprompt.Target{agentprompt.EvaluatorSystem},
			Apply: func(_ context.Context, document *agentprompt.Document, _ agentprompt.Context) error {
				return document.Add(agentprompt.SectionEvaluator, agentprompt.Static(evaluatorSystemPrompt))
			},
		},
		{
			Name:    "prompt.defaults.evaluator.request",
			Targets: []agentprompt.Target{agentprompt.EvaluatorRequest},
			Apply: func(_ context.Context, document *agentprompt.Document, _ agentprompt.Context) error {
				return document.Add(agentprompt.SectionRequest, renderEvaluatorRequest)
			},
		},
		{
			Name:    "prompt.defaults.compact.system",
			Targets: []agentprompt.Target{agentprompt.CompactSystem},
			Apply: func(_ context.Context, document *agentprompt.Document, _ agentprompt.Context) error {
				return document.Add(agentprompt.SectionCompact, agentprompt.Static(compactSystemPrompt))
			},
		},
		{
			Name:    "prompt.defaults.compact.request",
			Targets: []agentprompt.Target{agentprompt.CompactRequest},
			Apply: func(_ context.Context, document *agentprompt.Document, _ agentprompt.Context) error {
				return document.Add(agentprompt.SectionRequest, renderCompactRequest)
			},
		},
		{
			Name:    "prompt.defaults.compact.prefix",
			Targets: []agentprompt.Target{agentprompt.CompactPrefix},
			Apply: func(_ context.Context, document *agentprompt.Document, _ agentprompt.Context) error {
				return document.Add(agentprompt.SectionRequest, agentprompt.Static(compactPrefixPrompt))
			},
		},
	}
}

// buildAgentPrompt installs the neutral main-system skeleton. Domain
// extensions replace the identity and add their own sections on top of it.
func buildAgentPrompt(_ context.Context, document *agentprompt.Document, _ agentprompt.Context) error {
	sections := []struct {
		id     string
		render agentprompt.Renderer
	}{
		{agentprompt.SectionIdentity, renderIdentity},
		{agentprompt.SectionEnvironment, RenderEnvironment},
		{agentprompt.SectionTools, RenderTools},
		{agentprompt.SectionSkills, RenderSkills},
		{agentprompt.SectionLoadedSkills, RenderLoadedSkills},
	}
	for _, section := range sections {
		if err := document.Add(section.id, section.render); err != nil {
			return err
		}
	}
	return nil
}

func renderIdentity(_ context.Context, _ agentprompt.Context) (string, error) {
	return "You are the agent operating inside a cyber-harness runtime. Complete the task using the provided materials and tools.", nil
}

// RenderEnvironment renders the neutral environment section. Domain
// contributions reuse it when they build prompts outside the main skeleton.
func RenderEnvironment(_ context.Context, input agentprompt.Context) (string, error) {
	var out strings.Builder
	out.WriteString("## Environment\n\nOperating System: ")
	out.WriteString(input.Agent.OS)
	if input.Agent.Arch != "" {
		out.WriteByte('/')
		out.WriteString(input.Agent.Arch)
	}
	if !input.Agent.Now.IsZero() {
		out.WriteString("\nCurrent Time: ")
		out.WriteString(input.Agent.Now.Format("2006-01-02T15:04:05Z07:00"))
	}
	if input.Agent.Hostname != "" {
		out.WriteString("\nHostname: ")
		out.WriteString(input.Agent.Hostname)
	}
	if input.Agent.NodeName != "" {
		out.WriteString("\nNode: ")
		out.WriteString(input.Agent.NodeName)
	}
	if input.Agent.Windows {
		out.WriteString("\nShell: the bash tool accepts POSIX syntax. It runs native bash when Git or MSYS bash is installed, and cmd.exe only when bash is absent. Pseudo-commands run in-process; quote literal operators so they stay arguments.")
	}
	return out.String(), nil
}

// RenderTools renders the neutral tool list section.
func RenderTools(_ context.Context, input agentprompt.Context) (string, error) {
	if len(input.Agent.Tools) == 0 {
		return "", nil
	}
	var out strings.Builder
	out.WriteString("## Available Tools")
	for _, value := range input.Agent.Tools {
		fmt.Fprintf(&out, "\n\n### %s\n%s", value.Name, value.Description)
	}
	return out.String(), nil
}

// RenderSkills renders the neutral available-skills section.
func RenderSkills(_ context.Context, input agentprompt.Context) (string, error) {
	if len(input.Agent.Skills) == 0 {
		return "", nil
	}
	var out strings.Builder
	out.WriteString("## Available Skills\n\nThe following skills provide specialized instructions for capabilities and task domains.\nUse the read tool to load a skill file when the task matches its description.\nWhen a skill references relative paths, resolve them relative to the skill base directory.\n\n<available_skills>")
	for _, value := range input.Agent.Skills {
		fmt.Fprintf(&out, "\n  <skill>\n    <name>%s</name>\n    <description>%s</description>\n    <location>%s</location>\n  </skill>", escapeXMLText(value.Name), escapeXMLText(value.Description), escapeXMLText(value.Location))
	}
	out.WriteString("\n</available_skills>")
	return out.String(), nil
}

func escapeXMLText(value string) string {
	var out strings.Builder
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}

// RenderLoadedSkills renders the bodies of skills preloaded into the session.
func RenderLoadedSkills(_ context.Context, input agentprompt.Context) (string, error) {
	var out strings.Builder
	for _, value := range input.Agent.LoadedSkills {
		if strings.TrimSpace(value.Body) != "" {
			fmt.Fprintf(&out, "## Skill: %s\n\n%s\n\n", value.Name, value.Body)
		}
	}
	return strings.TrimSpace(out.String()), nil
}

func renderEvaluatorRequest(_ context.Context, input agentprompt.Context) (string, error) {
	var out strings.Builder
	fmt.Fprintf(&out, "## Goal\n%s\n\n", input.Evaluation.Goal)
	if input.Evaluation.Criteria != "" {
		fmt.Fprintf(&out, "## Acceptance Criteria\n%s\n\n", input.Evaluation.Criteria)
	}
	if input.Evaluation.Progress != "" {
		fmt.Fprintf(&out, "## Progress\n%s\n", input.Evaluation.Progress)
	}
	fmt.Fprintf(&out, "## Execution Trace\n%s", input.Evaluation.Trace)
	return out.String(), nil
}

func renderCompactRequest(_ context.Context, input agentprompt.Context) (string, error) {
	text := compactRequestPrompt
	if input.Compaction.CustomInstructions != "" {
		text += "\n\nAdditional focus: " + input.Compaction.CustomInstructions
	}
	return text, nil
}

const evaluatorSystemPrompt = `You are an evaluator. Call the "verdict" tool with your result. No text replies.

You own the stop decision: there is no small round budget to ration. The loop runs another round whenever you ask for one, so keep it alive while rounds are still buying progress, and end it yourself once they are not.

Rules:
- pass=true only if the task was fully achieved per criteria
- continue (only read when pass=false): true when another round has a concrete chance - the trace shows progress, or an untried step is available
- continue=false when the agent is stuck, blocked by something it cannot resolve, or the criteria cannot be met. Say which in reason
- continue=false is a normal outcome, not a failure to avoid
- follow user round guidance unless the goal is achieved or plainly stuck
- feedback: actionable next step when pass=false and continue=true
- inherit_context based on context_usage: >80% false; >50% normally false; <=50% normally true
- when inherit_context=false, feedback must be fully self-contained`

const compactSystemPrompt = `You are a context summarization assistant. Read the conversation and produce the requested structured summary. Do not continue the conversation or answer its questions.`

const compactRequestPrompt = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.

Use this exact format:

## Goal
[What the user is trying to accomplish.]

## Progress
### Done
- [x] [Completed work]

### In Progress
- [ ] [Current work]

## Key Decisions
- **[Decision]**: [Rationale]

## Next Steps
1. [Ordered next step]

## Critical Context
- [Paths, function names, errors, or other continuation data]

Keep each section concise and preserve exact technical identifiers.`

const compactPrefixPrompt = `This is the prefix of a turn that was too large to keep. The recent suffix is retained. Summarize the prefix using this exact format:

## Original Request
[What the user asked for]

## Early Progress
- [Important work and decisions]

## Context for Suffix
- [Information needed to understand the retained suffix]

Be concise.`
