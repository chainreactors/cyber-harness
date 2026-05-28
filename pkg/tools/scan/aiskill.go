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
	Agent   bool
	Accept  func(event) bool
	Prompt  func(event) string
	Workers int
}

var scanAISkills = []AISkill{
	{
		Name: "verify", CapName: capAgentVerify, Flag: "", Agent: true,
		Accept: shouldVerifyFinding,
		Prompt: func(e event) string {
			// Sniper findings carry CVE intelligence that needs active probing,
			// not just evidence review. Tailor the prompt accordingly.
			if ai, ok := e.Finding.(aiSkillFinding); ok && ai.Skill == "sniper" {
				return fmt.Sprintf(`CVE intelligence to actively verify against target:
- target: %s
- claimed CVEs/vulnerabilities: %s
- detail: %s

This is CVE lookup intelligence from fingerprint analysis, NOT a confirmed exploit.
Actively probe the target to determine if any of the claimed vulnerabilities are actually exploitable.
Check: service reachability, version confirmation, authentication requirements, WAF presence.
If active probing does not produce direct evidence of exploitability, return status "not_confirmed".
Use "inconclusive" only when probing cannot be completed or evaluated, not merely because access is denied.`,
					ai.Target, ai.Summary, ai.Detail)
			}
			return fmt.Sprintf(`Finding to verify:
- source: %s
- kind: %s
- priority: %s
- target: %s
- evidence: %s

Return "confirmed" only for direct evidence. Return "not_confirmed" when probing runs but does not support the claim.`,
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

func shouldVerifyFinding(e event) bool {
	if e.Kind != eventFinding || e.Finding == nil {
		return false
	}
	if e.Finding.Kind() == findingVerification {
		return false
	}
	if ai, ok := e.Finding.(aiSkillFinding); ok {
		return shouldVerifyAISkillFinding(ai)
	}
	return e.Finding.Priority().atLeast(priorityHigh)
}

func shouldVerifyAISkillFinding(finding aiSkillFinding) bool {
	switch finding.Skill {
	case "verify":
		return false
	case "sniper":
		return strings.TrimSpace(finding.Summary) != "" || strings.TrimSpace(finding.Detail) != ""
	default:
		return finding.Priority().atLeast(priorityHigh)
	}
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
	if skill.Agent {
		if c.agentFunc == nil {
			c.logger.Debugf("scan capability=%s status=skipped reason=agent_func_unconfigured", skill.CapName)
			return
		}
	} else if c.aiFunc == nil {
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
	if skillBody == "" {
		c.logger.Warnf("scan capability=%s skill=%s skill_body_empty, using fallback prompt", skill.CapName, skill.Name)
	}

	prompt := skill.Prompt(e)
	start := time.Now()

	var parsed *agent.SkillResult
	var rawResult string
	var err error

	if skill.Agent && c.agentFunc != nil {
		agentResult, agentErr := c.agentFunc(skillCtx, prompt, skillBody, c.aiConfig.Model, 1600)
		if agentErr != nil {
			c.logger.Debugf("scan capability=%s status=failed error=%q", skill.CapName, agentErr)
			return
		}
		if agentResult == nil {
			c.logger.Debugf("scan capability=%s status=failed error=nil_result", skill.CapName)
			return
		}
		parsed = agentResult.Parsed
		rawResult = agentResult.Raw
	} else {
		rawResult, err = c.aiFunc(skillCtx, prompt, skillBody, c.aiConfig.Model, 1600)
		if err != nil {
			c.logger.Debugf("scan capability=%s status=failed error=%q", skill.CapName, err)
			return
		}
		parsed, err = agent.ParseSkillResult(rawResult)
		if err != nil || parsed == nil {
			c.logger.Debugf("scan capability=%s status=parse_failed error=%q", skill.CapName, err)
			return
		}
	}
	duration := time.Since(start)

	if parsed == nil {
		c.logger.Debugf("scan capability=%s status=parse_failed error=nil_parsed", skill.CapName)
		return
	}

	if parsed.Summary == "" && parsed.Detail == "" {
		return
	}

	if c.recorder != nil {
		c.recorder.AITurn(skill.Name, 1, prompt, rawResult, nil, duration, record.TokenUsage{})
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

	// Use ReportMarkdown so the LLM sees verification annotations:
	// ~~strikethrough~~ for not_confirmed, **[verified]** for confirmed.
	// PlainText lacks these annotations, causing the LLM to treat
	// unverified fingerprints/sniper CVE lookups as confirmed vulns.
	scanData := coll.ReportMarkdown()
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
