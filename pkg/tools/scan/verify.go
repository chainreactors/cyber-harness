package scan

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/tools/scan/pipeline"
	"github.com/chainreactors/parsers"
)

func (c *Command) agentVerifyCapability(flags flags) (pipeline.Capability, bool) {
	minPriority, err := parsePriority(flags.Verify)
	if err != nil {
		minPriority = priorityHigh
	}
	workers := c.verification.Workers
	if workers <= 0 {
		workers = 3
	}
	accept := func(e event) bool {
		if e.Kind != eventFinding || e.Finding == nil {
			return false
		}
		if e.Finding.Kind() == findingVerification {
			return false
		}
		return e.Finding.Priority().atLeast(minPriority)
	}
	cap := wrapCapability(
		capAgentVerify,
		accept,
		workers,
		func(ctx context.Context, e event, emit func(event)) {
			c.runAgentVerifyCapability(ctx, flags, e, emit)
		},
	)
	cap.RunKey = func(pe pipeline.Event) string {
		e, ok := pe.(event)
		if !ok || e.Finding == nil {
			return ""
		}
		return capAgentVerify + "|" + string(e.Finding.Kind()) + "|" + e.Finding.Key()
	}
	return cap, true
}

func (c *Command) runAgentVerifyCapability(ctx context.Context, flags flags, event event, emit func(event)) {
	if c.verifyFunc == nil {
		c.logger.Debugf("scan capability=%s status=skipped reason=provider_unconfigured finding=%s key=%q", capAgentVerify, findingKindOf(event.Finding), findingKey(event.Finding))
		return
	}

	timeout := flags.VerifyTimeout
	if timeout <= 0 {
		timeout = c.verification.Timeout
	}
	if timeout <= 0 {
		timeout = 120
	}
	verifyCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	model := strings.TrimSpace(c.verification.Model)
	prompt := buildVerificationPrompt(event)
	systemPrompt := strings.TrimSpace(c.verification.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = defaultVerifySystemPrompt
	}

	result, err := c.verifyFunc(verifyCtx, prompt, systemPrompt, model, 1200)
	if err != nil {
		c.logger.Debugf("scan capability=%s status=failed finding=%s key=%q error=%q", capAgentVerify, findingKindOf(event.Finding), findingKey(event.Finding), err)
		return
	}

	status, summary, evidence := parseVerificationOutput(result)

	if status == verificationInconclusive && !strings.Contains(strings.ToLower(result), "status:") {
		rawPreview := result
		if len(rawPreview) > 200 {
			rawPreview = rawPreview[:200]
		}
		c.logger.Debugf("scan capability=%s status=parse_unclear action=retry raw=%q", capAgentVerify, rawPreview)
		retryPrompt := prompt + "\n\nPlease follow the exact output format with status:/summary:/evidence: lines."
		retryResult, retryErr := c.verifyFunc(verifyCtx, retryPrompt, systemPrompt, model, 1200)
		if retryErr == nil {
			status, summary, evidence = parseVerificationOutput(retryResult)
		}
	}
	if status != verificationConfirmed {
		c.logger.Debugf("scan capability=%s status=%s finding=%s key=%q target=%q summary=%q evidence=%q", capAgentVerify, status, findingKindOf(event.Finding), findingKey(event.Finding), findingTarget(event.Finding), summary, evidence)
		return
	}

	emit(findingEvent(capAgentVerify, verificationFinding{
		OriginalKey:      findingKey(event.Finding),
		OriginalKind:     findingKindOf(event.Finding),
		OriginalPriority: findingPriority(event.Finding),
		Status:           status,
		Target:           findingTarget(event.Finding),
		Summary:          summary,
		Evidence:         evidence,
	}))
}

func buildVerificationPrompt(event event) string {
	finding := event.Finding
	return fmt.Sprintf(`Verify this scan finding from already-collected scanner evidence.

Finding:
- source: %s
- kind: %s
- priority: %s
- key: %s
- target: %s
- evidence: %s

Return only this plain text format:
status: confirmed|not_confirmed|inconclusive
summary: one concise sentence
evidence: short evidence from the provided finding or why it is insufficient; for focus fingerprints, include historical-vulnerability relevance or safe validation guidance when possible

Examples:

Example 1 (confirmed):
Finding: source=neutron_poc kind=vuln-finding priority=high target=10.0.0.1 evidence=[vuln] 10.0.0.1 CVE-2021-44228 critical Apache Log4j RCE
Response:
status: confirmed
summary: Log4j RCE (CVE-2021-44228) confirmed by matched POC template with critical severity.
evidence: Neutron template matched CVE-2021-44228 on target, severity=critical.

Example 2 (not_confirmed):
Finding: source=spray_check kind=fingerprint priority=low target=10.0.0.2 evidence=fingerprint jquery
Response:
status: not_confirmed
summary: jQuery fingerprint alone does not indicate a security risk without a specific CVE.
evidence: Fingerprint detection only; no vulnerability evidence provided.`,
		event.Source,
		findingKindOf(finding),
		findingPriority(finding),
		findingKey(finding),
		findingTarget(finding),
		findingEvidence(finding),
	)
}

const defaultVerifySystemPrompt = `You are aiscan's verification reviewer. Validate only the supplied scanner finding and evidence. Do not invent external facts, do not request tools, and do not perform additional scanning. Mark confirmed only when the evidence directly supports the risk. For focus fingerprints, assess likely historical-vulnerability relevance and suggest safe non-destructive validation, but do not mark confirmed from fingerprint evidence alone.`

func parseVerificationOutput(output string) (verificationStatus, string, string) {
	output = strings.TrimSpace(output)
	if output == "" {
		return verificationInconclusive, "empty verification response", ""
	}
	status := verificationInconclusive
	summary := output
	evidence := ""
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "status":
			status = normalizeVerificationStatus(value)
		case "summary":
			if value != "" {
				summary = value
			}
		case "evidence":
			evidence = value
		}
	}
	return status, oneLine(summary), oneLine(evidence)
}

func normalizeVerificationStatus(value string) verificationStatus {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(verificationConfirmed):
		return verificationConfirmed
	case string(verificationNotConfirmed), "not confirmed", "false_positive", "false-positive":
		return verificationNotConfirmed
	case string(verificationFailed), "error":
		return verificationFailed
	default:
		return verificationInconclusive
	}
}

func findingKindOf(finding finding) findingKind {
	if finding == nil {
		return ""
	}
	return finding.Kind()
}

func findingKey(finding finding) string {
	if finding == nil {
		return ""
	}
	return finding.Key()
}

func findingPriority(finding finding) priority {
	if finding == nil {
		return priorityLow
	}
	return finding.Priority()
}

func findingTarget(finding finding) string {
	switch f := finding.(type) {
	case fingerprintFinding:
		return f.Target
	case weakpassFinding:
		if f.Result != nil {
			return f.Result.URI()
		}
	case vulnFinding:
		return f.Target
	case verificationFinding:
		return f.Target
	}
	return ""
}

func findingEvidence(finding finding) string {
	switch f := finding.(type) {
	case fingerprintFinding:
		prefix := "fingerprint "
		if f.Focus {
			prefix = "focus fingerprint "
		}
		return strings.TrimSpace(prefix + strings.Join(parsers.NormalizeNames(f.Fingers), ","))
	case weakpassFinding:
		if f.Result != nil {
			return f.Result.OutputLine()
		}
	case vulnFinding:
		return f.String()
	case verificationFinding:
		return f.Summary
	}
	return ""
}

func oneLine(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.Join(strings.Fields(value), " ")
}
