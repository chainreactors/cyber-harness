package scan

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/chainreactors/aiscan/pkg/util"
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
	source := event.Source
	if source == "" || source == "input" {
		source = "scan"
	}
	prefix := colorize(color, ansiDim, "["+source+"]")

	switch event.Kind {
	case eventTarget:
		switch target := event.Target.(type) {
		case serviceTarget:
			if target.Result == nil {
				return ""
			}
			return sanitizeOutputLine(fmt.Sprintf("%s %s", prefix, formatServiceResult(target.Result, color)), color)
		case webTarget:
			if target.URL == "" {
				return ""
			}
			parts := []string{prefix, colorize(color, ansiBold+ansiGreen, target.URL)}
			if target.HostHeader != "" {
				parts = append(parts, colorize(color, ansiDim, "("+target.HostHeader+")"))
			}
			return sanitizeOutputLine(strings.Join(parts, " "), color)
		case webProbeTarget:
			if !reportableSprayResult(target.Result) {
				return ""
			}
			return sanitizeOutputLine(fmt.Sprintf("%s %s", prefix, formatSprayResult(target.Result, color)), color)
		}
	case eventFinding:
		switch finding := event.Finding.(type) {
		case fingerprintFinding:
			names := parsers.NormalizeNames(finding.Fingers)
			if len(names) == 0 {
				return ""
			}
			return formatPriorityLine(prefix, finding.Priority(), "fingerprint", finding.Target, []string{
				colorize(color, ansiCyan, strings.Join(names, ",")),
			}, color)
		case weakpassFinding:
			if finding.Result == nil {
				return ""
			}
			return formatPriorityLine(prefix, finding.Priority(), "weakpass", finding.Result.URI(), weakpassFields(finding.Result), color)
		case vulnFinding:
			if finding.Message == "" {
				return ""
			}
			return formatPriorityLine(prefix, finding.Priority(), "vuln", vulnTarget(finding.Message), []string{
				util.FormatValue(finding.Message),
			}, color)
		case verificationFinding:
			return formatPriorityLine(prefix, finding.Priority(), "verify", finding.Target, verificationFields(finding), color)
		}
	case eventError:
		if event.Error.Message == "" {
			return ""
		}
		return sanitizeOutputLine(fmt.Sprintf("%s %s %s", prefix, colorize(color, ansiRed, "error"), util.FormatValue(event.Error.Message)), color)
	}
	return ""
}

func formatPriorityLine(prefix string, priority priority, label, target string, fields []string, color bool) string {
	priorityText := strings.ToUpper(string(priority))
	if priorityText == "" {
		priorityText = "INFO"
	}
	parts := []string{prefix}
	if target != "" {
		parts = append(parts, colorize(color, ansiBold+ansiGreen, target))
	}
	parts = append(parts,
		colorize(color, colorForPriority(priority), label),
		colorize(color, colorForPriority(priority), strings.ToLower(priorityText)),
	)
	parts = append(parts, fields...)
	return sanitizeOutputLine(strings.Join(parts, " "), color)
}

func sanitizeOutputLine(line string, color bool) string {
	line = strings.TrimSpace(line)
	if !color {
		line = stripANSI(line)
	}
	return line
}

func formatServiceResult(result *parsers.GOGOResult, color bool) string {
	target := result.GetTarget()
	if result.IsHttp() {
		target = result.GetBaseURL()
	}
	parts := []string{
		colorize(color, ansiBold+ansiGreen, target),
	}
	if result.Timing > 0 {
		parts = append(parts, colorize(color, ansiYellow, fmt.Sprintf("%dms", result.Timing)))
	}
	parts = appendNonEmptyColoredValue(parts, result.Protocol, ansiDim, color)
	parts = appendNonEmptyColoredValue(parts, result.Status, colorForStatus(result.Status), color)
	parts = appendNonEmptyColoredValue(parts, result.Midware, ansiCyan, color)
	parts = appendNonEmptyColoredValue(parts, result.Host, ansiDim, color)
	parts = appendNonEmptyColoredValue(parts, result.Title, ansiGreen, color)
	if frameworks := strings.Trim(result.Frameworks.String(), "|"); frameworks != "" {
		parts = append(parts, colorize(color, colorForFrameworks(result.Frameworks.IsFocus()), util.FormatValue(frameworks)))
	}
	if vulns := strings.TrimSpace(result.Vulns.String()); vulns != "" {
		parts = append(parts, colorize(color, ansiRed, util.FormatValue(vulns)))
	}
	if extract := strings.TrimSpace(result.GetExtractStat()); extract != "" {
		parts = append(parts, colorize(color, ansiCyan, util.FormatValue(extract)))
	}
	return strings.Join(parts, " ")
}

func formatSprayResult(result *parsers.SprayResult, color bool) string {
	parts := []string{
		colorize(color, ansiCyan, result.Source.Name()),
	}
	if result.Status > 0 {
		status := strconv.Itoa(result.Status)
		parts = append(parts, colorize(color, colorForStatus(status), status))
	}
	parts = append(parts, colorize(color, ansiYellow, strconv.Itoa(result.BodyLength)))
	if result.ExceedLength {
		parts = append(parts, colorize(color, ansiRed, "exceed"))
	}
	if result.Spended > 0 {
		parts = append(parts, colorize(color, ansiYellow, fmt.Sprintf("%dms", result.Spended)))
	}
	parts = append(parts, colorize(color, ansiBold+ansiGreen, result.UrlString))
	if result.Host != "" {
		parts = append(parts, colorize(color, ansiDim, "("+result.Host+")"))
	}
	if result.RedirectURL != "" {
		parts = append(parts, colorize(color, ansiCyan, "->"), colorize(color, ansiCyan, result.RedirectURL))
	}
	parts = appendNonEmptyColoredValue(parts, result.Title, ansiGreen, color)
	if result.Distance != 0 {
		parts = append(parts, colorize(color, ansiGreen, strconv.Itoa(int(result.Distance))))
	}
	parts = appendNonEmptyColoredValue(parts, result.Reason, ansiYellow, color)
	if frameworks := strings.TrimSpace(result.Get("frame")); frameworks != "" {
		parts = append(parts, colorize(color, colorForFrameworks(result.Frameworks.IsFocus()), strings.TrimSpace(frameworks)))
	}
	if extracts := strings.TrimSpace(result.Extracteds.String()); extracts != "" {
		parts = append(parts, colorize(color, ansiCyan, util.FormatValue(extracts)))
	}
	return strings.Join(parts, " ")
}

func colorForStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return ansiDim
	}
	code, err := strconv.Atoi(status)
	if err != nil {
		if strings.EqualFold(status, "open") || strings.EqualFold(status, "tcp") {
			return ansiGreen
		}
		return ansiDim
	}
	switch {
	case code >= 200 && code < 300:
		return ansiGreen
	case code >= 300 && code < 400:
		return ansiCyan
	case code >= 400 && code < 500:
		return ansiYellow
	case code >= 500:
		return ansiRed
	default:
		return ansiDim
	}
}

func colorForFrameworks(hasFocus bool) string {
	if hasFocus {
		return ansiBold + ansiRed
	}
	return ansiCyan
}

func weakpassFields(result *parsers.ZombieResult) []string {
	fields := make([]string, 0, 4)
	fields = appendNonEmptyValue(fields, result.Username)
	fields = appendNonEmptyValue(fields, result.Password)
	fields = appendNonEmptyValue(fields, result.Service)
	fields = appendNonEmptyValue(fields, result.Mod.String())
	return fields
}

func verificationFields(finding verificationFinding) []string {
	parts := []string{util.FormatValue(string(finding.Status))}
	if finding.OriginalPriority != "" {
		parts = append(parts, util.FormatValue(string(finding.OriginalPriority)))
	}
	if finding.OriginalKind != "" {
		parts = append(parts, util.FormatValue(string(finding.OriginalKind)))
	}
	parts = appendNonEmptyValue(parts, finding.Summary)
	parts = appendNonEmptyValue(parts, finding.Evidence)
	return parts
}

func appendNonEmptyValue(parts []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return parts
	}
	return append(parts, util.FormatValue(value))
}

func appendNonEmptyColoredValue(parts []string, value, code string, color bool) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return parts
	}
	return append(parts, colorize(color, code, util.FormatValue(value)))
}
