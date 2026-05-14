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

type outputOptions struct {
	Color bool
}

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

func formatEventLine(event event, opts outputOptions) string {
	source := event.Source
	if source == "" || source == "input" {
		source = "scan"
	}
	prefix := colorize(opts.Color, ansiDim, "["+source+"]")

	switch event.Kind {
	case eventTarget:
		switch target := event.Target.(type) {
		case serviceTarget:
			if target.Result == nil {
				return ""
			}
			return sanitizeOutputLine(fmt.Sprintf("%s %s", prefix, formatServiceResult(target.Result, opts)), opts)
		case webTarget:
			if target.URL == "" {
				return ""
			}
			parts := []string{prefix, colorize(opts.Color, ansiBold+ansiGreen, target.URL)}
			if target.HostHeader != "" {
				parts = append(parts, colorize(opts.Color, ansiDim, "("+target.HostHeader+")"))
			}
			return sanitizeOutputLine(strings.Join(parts, " "), opts)
		case webProbeTarget:
			if !reportableSprayResult(target.Result) {
				return ""
			}
			return sanitizeOutputLine(fmt.Sprintf("%s %s", prefix, formatSprayResult(target.Result, opts)), opts)
		}
	case eventFinding:
		switch finding := event.Finding.(type) {
		case fingerprintFinding:
			names := parsers.NormalizeNames(finding.Fingers)
			if len(names) == 0 {
				return ""
			}
			return formatPriorityLine(prefix, finding.Priority(), "fingerprint", finding.Target, []string{
				colorize(opts.Color, ansiCyan, strings.Join(names, ",")),
			}, opts)
		case weakpassFinding:
			if finding.Result == nil {
				return ""
			}
			return formatPriorityLine(prefix, finding.Priority(), "weakpass", finding.Result.URI(), weakpassFields(finding.Result), opts)
		case vulnFinding:
			if finding.Message == "" {
				return ""
			}
			return formatPriorityLine(prefix, finding.Priority(), "vuln", vulnTarget(finding.Message), []string{
				util.FormatValue(finding.Message),
			}, opts)
		case verificationFinding:
			return formatPriorityLine(prefix, finding.Priority(), "verify", finding.Target, verificationFields(finding), opts)
		}
	case eventError:
		if event.Error.Message == "" {
			return ""
		}
		return sanitizeOutputLine(fmt.Sprintf("%s %s %s", prefix, colorize(opts.Color, ansiRed, "error"), util.FormatValue(event.Error.Message)), opts)
	}
	return ""
}

func formatPriorityLine(prefix string, priority priority, label, target string, fields []string, opts outputOptions) string {
	priorityText := strings.ToUpper(string(priority))
	if priorityText == "" {
		priorityText = "INFO"
	}
	parts := []string{prefix}
	if target != "" {
		parts = append(parts, colorize(opts.Color, ansiBold+ansiGreen, target))
	}
	parts = append(parts,
		colorize(opts.Color, colorForPriority(priority), label),
		colorize(opts.Color, colorForPriority(priority), strings.ToLower(priorityText)),
	)
	parts = append(parts, fields...)
	return sanitizeOutputLine(strings.Join(parts, " "), opts)
}

func sanitizeOutputLine(line string, opts outputOptions) string {
	line = strings.TrimSpace(line)
	if !opts.Color {
		line = stripANSI(line)
	}
	return line
}

func formatServiceResult(result *parsers.GOGOResult, opts outputOptions) string {
	target := result.GetTarget()
	if result.IsHttp() {
		target = result.GetBaseURL()
	}
	parts := []string{
		colorize(opts.Color, ansiBold+ansiGreen, target),
	}
	if result.Timing > 0 {
		parts = append(parts, colorize(opts.Color, ansiYellow, fmt.Sprintf("%dms", result.Timing)))
	}
	parts = appendNonEmptyColoredValue(parts, result.Protocol, ansiDim, opts)
	parts = appendNonEmptyColoredValue(parts, result.Status, colorForStatus(result.Status), opts)
	parts = appendNonEmptyColoredValue(parts, result.Midware, ansiCyan, opts)
	parts = appendNonEmptyColoredValue(parts, result.Host, ansiDim, opts)
	parts = appendNonEmptyColoredValue(parts, result.Title, ansiGreen, opts)
	if frameworks := strings.Trim(result.Frameworks.String(), "|"); frameworks != "" {
		parts = append(parts, colorize(opts.Color, colorForFrameworks(result.Frameworks.IsFocus()), util.FormatValue(frameworks)))
	}
	if vulns := strings.TrimSpace(result.Vulns.String()); vulns != "" {
		parts = append(parts, colorize(opts.Color, ansiRed, util.FormatValue(vulns)))
	}
	if extract := strings.TrimSpace(result.GetExtractStat()); extract != "" {
		parts = append(parts, colorize(opts.Color, ansiCyan, util.FormatValue(extract)))
	}
	return strings.Join(parts, " ")
}

func formatSprayResult(result *parsers.SprayResult, opts outputOptions) string {
	parts := []string{
		colorize(opts.Color, ansiCyan, result.Source.Name()),
	}
	if result.Status > 0 {
		status := strconv.Itoa(result.Status)
		parts = append(parts, colorize(opts.Color, colorForStatus(status), status))
	}
	parts = append(parts, colorize(opts.Color, ansiYellow, strconv.Itoa(result.BodyLength)))
	if result.ExceedLength {
		parts = append(parts, colorize(opts.Color, ansiRed, "exceed"))
	}
	if result.Spended > 0 {
		parts = append(parts, colorize(opts.Color, ansiYellow, fmt.Sprintf("%dms", result.Spended)))
	}
	parts = append(parts, colorize(opts.Color, ansiBold+ansiGreen, result.UrlString))
	if result.Host != "" {
		parts = append(parts, colorize(opts.Color, ansiDim, "("+result.Host+")"))
	}
	if result.RedirectURL != "" {
		parts = append(parts, colorize(opts.Color, ansiCyan, "->"), colorize(opts.Color, ansiCyan, result.RedirectURL))
	}
	parts = appendNonEmptyColoredValue(parts, result.Title, ansiGreen, opts)
	if result.Distance != 0 {
		parts = append(parts, colorize(opts.Color, ansiGreen, strconv.Itoa(int(result.Distance))))
	}
	parts = appendNonEmptyColoredValue(parts, result.Reason, ansiYellow, opts)
	if frameworks := strings.TrimSpace(result.Get("frame")); frameworks != "" {
		parts = append(parts, colorize(opts.Color, colorForFrameworks(result.Frameworks.IsFocus()), strings.TrimSpace(frameworks)))
	}
	if extracts := strings.TrimSpace(result.Extracteds.String()); extracts != "" {
		parts = append(parts, colorize(opts.Color, ansiCyan, util.FormatValue(extracts)))
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

func appendNonEmptyColoredValue(parts []string, value, code string, opts outputOptions) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return parts
	}
	return append(parts, colorize(opts.Color, code, util.FormatValue(value)))
}
