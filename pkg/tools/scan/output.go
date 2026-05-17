package scan

import (
	"regexp"
	"strings"

	"github.com/chainreactors/parsers"
)

const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[2m"
	ansiCyan   = "\x1b[36m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
	ansiBold   = "\x1b[1m"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripANSI(value string) string {
	return ansiPattern.ReplaceAllString(value, "")
}

func colorize(enabled bool, code, value string) string {
	if !enabled || value == "" {
		return value
	}
	return code + value + ansiReset
}

func colorForPriority(priority priority) string {
	switch priority {
	case priorityLow:
		return ansiCyan
	case priorityMedium:
		return ansiYellow
	case priorityHigh:
		return ansiRed
	case priorityCritical:
		return ansiBold + ansiRed
	default:
		return ansiDim
	}
}

func formatEventLine(event event, color bool) string {
	switch event.Kind {
	case eventTarget:
		switch target := event.Target.(type) {
		case serviceTarget:
			if target.Result == nil {
				return ""
			}
			label := "service"
			if target.Result.IsHttp() {
				label = "web"
			}
			prefix := outputPrefix(label, ansiGreen, color)
			return formatOutputLine(prefix, target.Result.OutputLine(), color)
		case webTarget:
			if target.URL == "" {
				return ""
			}
			prefix := outputPrefix("web", ansiGreen, color)
			return formatOutputLine(prefix, parsers.JoinOutput(target.URL, target.HostHeader), color)
		case webProbeTarget:
			if !reportableSprayResultForCapability(target.Result, target.Capability) {
				return ""
			}
			prefix := outputPrefix("web", ansiGreen, color)
			return formatOutputLine(prefix, target.Result.OutputLine(), color)
		}
	case eventFinding:
		switch finding := event.Finding.(type) {
		case fingerprintFinding:
			names := parsers.NormalizeNames(finding.Fingers)
			if len(names) == 0 || !finding.Focus {
				return ""
			}
			prefix := outputPrefix("fingerprint", colorForPriority(finding.Priority()), color)
			return formatOutputLine(prefix, parsers.JoinOutput(finding.Target, parsers.NamesOutput(names)), color)
		case weakpassFinding:
			if finding.Result == nil {
				return ""
			}
			prefix := outputPrefix("risk", colorForPriority(finding.Priority()), color)
			return formatOutputLine(prefix, finding.Result.OutputLine(), color)
		case vulnFinding:
			if finding.String() == "" {
				return ""
			}
			prefix := outputPrefix("vuln", colorForPriority(finding.Priority()), color)
			return formatOutputLine(prefix, finding.String(), color)
		case verificationFinding:
			if !reportableVerificationFinding(finding) {
				return ""
			}
			prefix := outputPrefix("ai", colorForVerificationStatus(finding.Status), color)
			return formatOutputLine(prefix, verificationOutput(finding), color)
		case aiSkillFinding:
			if finding.Summary == "" && finding.Detail == "" {
				return ""
			}
			prefix := outputPrefix(aiSkillOutputLabel(finding.Skill), aiSkillOutputColor(finding), color)
			return formatOutputLine(prefix, aiSkillOutput(finding), color)
		}
	case eventError:
		if event.Error.Message == "" {
			return ""
		}
		prefix := outputPrefix("error", ansiRed, color)
		return formatOutputLine(prefix, parsers.JoinOutput(event.Error.Message), color)
	}
	return ""
}

func outputPrefix(source, code string, color bool) string {
	return colorize(color, code, "["+source+"]")
}


func formatOutputLine(prefix, output string, color bool) string {
	output = strings.TrimSpace(output)
	parts := []string{prefix}
	if output != "" {
		parts = append(parts, output)
	}
	return sanitizeOutputLine(strings.Join(parts, " "), color)
}

func sanitizeOutputLine(line string, color bool) string {
	line = strings.TrimSpace(line)
	if !color {
		line = stripANSI(line)
	}
	return line
}

func colorForVerificationStatus(status verificationStatus) string {
	switch status {
	case verificationConfirmed:
		return ansiGreen
	case verificationNotConfirmed, verificationFailed:
		return ansiRed
	default:
		return ansiYellow
	}
}

func verificationOutput(finding verificationFinding) string {
	return parsers.JoinOutput(
		finding.Target,
		string(finding.OriginalKind),
		string(finding.Status),
		finding.Summary,
		finding.Evidence,
	)
}

func aiSkillOutputLabel(skill string) string {
	if skill == "verify" {
		return "ai"
	}
	return skill
}

func aiSkillOutputColor(finding aiSkillFinding) string {
	switch finding.Status {
	case "confirmed":
		return ansiGreen
	case "not_confirmed":
		return ansiDim
	case "info":
		return ansiYellow
	default:
		return ansiYellow
	}
}

func aiSkillOutput(finding aiSkillFinding) string {
	parts := []string{finding.Target}
	if finding.Status != "" {
		parts = append(parts, finding.Status)
	}
	if finding.Summary != "" {
		parts = append(parts, finding.Summary)
	}
	if finding.Detail != "" {
		parts = append(parts, finding.Detail)
	}
	return parsers.JoinOutput(parts...)
}
