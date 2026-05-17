package scan

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/record"
	"github.com/chainreactors/aiscan/pkg/tools/scan/pipeline"
)

type AISkill struct {
	Name    string
	CapName string
	Flag    string
	Accept  func(event) bool
	Prompt  func(event) string
	Workers int
}

var scanAISkills = []AISkill{
	{
		Name: "verify", CapName: capAgentVerify, Flag: "",
		Accept: func(e event) bool {
			if e.Kind != eventFinding || e.Finding == nil {
				return false
			}
			if e.Finding.Kind() == findingAISkill || e.Finding.Kind() == findingVerification {
				return false
			}
			return e.Finding.Priority().atLeast(priorityHigh)
		},
		Prompt: func(e event) string {
			return fmt.Sprintf(`Verify this scan finding:
- source: %s
- kind: %s
- priority: %s
- target: %s
- evidence: %s

Determine whether this is a real confirmed security issue.`,
				e.Source, findingKindOf(e.Finding), findingPriority(e.Finding),
				findingTarget(e.Finding), findingEvidence(e.Finding))
		},
		Workers: 3,
	},
	{
		Name: "sniper", CapName: capAgentSniper, Flag: "sniper",
		Accept: func(e event) bool {
			if e.Kind != eventFinding {
				return false
			}
			fp, ok := e.Finding.(fingerprintFinding)
			return ok && fp.Focus
		},
		Prompt: func(e event) string {
			fp, _ := e.Finding.(fingerprintFinding)
			return fmt.Sprintf(`Search for known public vulnerabilities for fingerprints on target %s.

Fingerprints: %s

Identify known CVEs (critical/high), public exploits, and remediation.`,
				fp.Target, strings.Join(fp.Fingers, ", "))
		},
		Workers: 3,
	},
}

func aiSkillEnabled(skill AISkill, flags flags) bool {
	if flags.AI {
		return true
	}
	switch skill.Flag {
	case "sniper":
		return flags.Sniper
	case "":
		return verificationEnabled(flags.Verify)
	default:
		return false
	}
}

func buildAISkillCap(c *Command, skill AISkill) pipeline.Capability {
	workers := skill.Workers
	if c.aiConfig.Workers > 0 {
		workers = c.aiConfig.Workers
	}
	if workers <= 0 {
		workers = 3
	}

	cap := wrapCapability(skill.CapName, skill.Accept, workers,
		func(ctx context.Context, e event, emit func(event)) {
			c.runAISkill(ctx, skill, e, emit)
		},
	)
	cap.RunKey = func(pe pipeline.Event) string {
		e, ok := pe.(event)
		if !ok || e.Finding == nil {
			return ""
		}
		return skill.CapName + "|" + string(e.Finding.Kind()) + "|" + e.Finding.Key()
	}
	return cap
}

func (c *Command) runAISkill(ctx context.Context, skill AISkill, e event, emit func(event)) {
	if c.aiFunc == nil {
		c.logger.Debugf("scan capability=%s status=skipped reason=provider_unconfigured", skill.CapName)
		return
	}

	timeout := c.aiConfig.Timeout
	if timeout <= 0 {
		timeout = 300
	}
	skillCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	skillBody := ""
	if c.skillStore != nil {
		skillBody = c.skillStore.LoadBody(skill.Name)
	}

	prompt := skill.Prompt(e)
	start := time.Now()
	result, err := c.aiFunc(skillCtx, prompt, skillBody, c.aiConfig.Model, 1600)
	duration := time.Since(start)

	if err != nil {
		c.logger.Debugf("scan capability=%s status=failed error=%q", skill.CapName, err)
		return
	}

	if c.recorder != nil {
		c.recorder.AITurn(skill.Name, 1, prompt, result, nil, duration, record.TokenUsage{})
	}

	parsed, err := agent.ParseSkillResult(result)
	if err != nil || parsed == nil {
		c.logger.Debugf("scan capability=%s status=parse_failed error=%q", skill.CapName, err)
		return
	}

	if parsed.Summary == "" && parsed.Detail == "" {
		return
	}

	originalKey := ""
	originalKind := findingKind("")
	if e.Finding != nil {
		originalKey = e.Finding.Key()
		originalKind = e.Finding.Kind()
	}
	if parsed.Target == "" {
		parsed.Target = findingTarget(e.Finding)
	}

	if c.recorder != nil {
		c.recorder.AISkill(skill.Name, parsed.Target, parsed.Status, parsed.Summary, parsed.Detail, duration)
	}

	emit(findingEvent(skill.CapName, aiSkillFinding{
		Skill:        skill.Name,
		Target:       parsed.Target,
		Status:       parsed.Status,
		Summary:      parsed.Summary,
		Detail:       parsed.Detail,
		Remediation:  parsed.Remediation,
		OriginalKey:  originalKey,
		OriginalKind: originalKind,
	}))
}

func (c *Command) generateAIReport(ctx context.Context, coll *collector) string {
	var fn func(ctx context.Context, prompt, systemPrompt, model string, maxTokens int) (string, error)
	if c.reportFunc != nil {
		fn = c.reportFunc
	} else if c.aiFunc != nil {
		fn = c.aiFunc
	}
	if fn == nil {
		return coll.ReportMarkdown()
	}

	scanData := coll.PlainText()
	if scanData == "" {
		scanData = coll.TerminalString(false)
	}

	skillBody := ""
	if c.skillStore != nil {
		skillBody = c.skillStore.LoadBody("report")
	}

	prompt := fmt.Sprintf(`Generate a security scan report from the following scan results and write it to a file with a descriptive name (e.g., report_<target>_<date>.md).

After writing the file, output the report content to the user as well.

Scan results:
%s`, scanData)

	timeout := c.aiConfig.Timeout
	if timeout <= 0 {
		timeout = 300
	}
	reportCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	result, err := fn(reportCtx, prompt, skillBody, c.aiConfig.Model, 4000)
	if err != nil {
		c.logger.Debugf("scan report skill failed: %v", err)
		return coll.ReportMarkdown()
	}

	result = strings.TrimSpace(result)
	if result == "" {
		return coll.ReportMarkdown()
	}
	return result + "\n"
}
