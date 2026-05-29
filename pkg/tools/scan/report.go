package scan

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chainreactors/parsers"
	sdktypes "github.com/chainreactors/sdk/pkg/types"
)

func formatSummary(d *collector, color bool) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	stats := d.statsSnapshotLocked()

	var sb strings.Builder
	if d.stream == nil {
		for _, line := range d.fileLines {
			sb.WriteString(sanitizeOutputLine(line, color))
			sb.WriteString("\n")
		}
	}
	sb.WriteString(formatScanSummaryLine(d, stats, color))

	if len(d.trace) > 0 {
		for _, line := range d.trace {
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

func formatMarkdown(d *collector) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	stats := d.statsSnapshotLocked()

	var sb strings.Builder
	sb.WriteString("# Scan Report\n\n")
	sb.WriteString(formatScanSummaryLine(d, stats, false))
	sb.WriteString("\n\n")

	sb.WriteString("## Metrics\n\n")
	sb.WriteString("| Metric | Value |\n")
	sb.WriteString("| --- | ---: |\n")
	sb.WriteString(fmt.Sprintf("| Inputs | %d |\n", stats.Inputs))
	sb.WriteString(fmt.Sprintf("| Open services | %d |\n", len(d.gogoResults)))
	sb.WriteString(fmt.Sprintf("| Web endpoints | %d |\n", len(d.webEndpoints)))
	sb.WriteString(fmt.Sprintf("| Web probes | %d |\n", len(d.sprayResults)))
	sb.WriteString(fmt.Sprintf("| Fingerprints | %d |\n", len(d.fingerprints)))
	sb.WriteString(fmt.Sprintf("| Weakpass findings | %d |\n", len(d.zombieResults)))
	sb.WriteString(fmt.Sprintf("| Vulnerability findings | %d |\n", len(d.neutronMatches)))
	sb.WriteString(fmt.Sprintf("| AI verifications | %d |\n", d.confirmedVerificationCountLocked()))
	sb.WriteString(fmt.Sprintf("| AI skill findings | %d |\n", len(d.aiSkillResults)))
	sb.WriteString(fmt.Sprintf("| Errors | %d |\n", len(d.errors)))
	sb.WriteString(fmt.Sprintf("| Tasks | %d |\n", stats.Tasks))
	sb.WriteString(fmt.Sprintf("| Requests | %d |\n", stats.Requests))
	sb.WriteString(fmt.Sprintf("| Duration | %s |\n", stats.Duration().Round(time.Millisecond)))

	if d.debug && len(stats.CapabilityRuns) > 0 {
		sb.WriteString("\n## Capability Runs\n\n")
		writeCountTable(&sb, "Capability", stats.CapabilityRuns)
	}

	if d.debug && len(stats.EngineStats) > 0 {
		sb.WriteString("\n## Engine Stats\n\n")
		writeEngineStatsTable(&sb, stats.EngineStats)
	}

	if len(d.gogoResults) > 0 {
		sb.WriteString("\n## Open Services\n\n")
		for _, result := range sortedCopy(d.gogoResults, func(a, b *parsers.GOGOResult) bool {
			return a.GetTarget() < b.GetTarget()
		}) {
			writeMarkdownEventLine(&sb, targetEvent(capGogoPortscan, "", newServiceTarget("", result)))
		}
	}

	if len(d.webEndpoints) > 0 {
		sb.WriteString("\n## Web Endpoints\n\n")
		for _, endpoint := range sortedCopy(d.webEndpoints, func(a, b webEndpoint) bool {
			if a.URL == b.URL {
				return a.HostHeader < b.HostHeader
			}
			return a.URL < b.URL
		}) {
			writeMarkdownEventLine(&sb, targetEvent(endpoint.Source, "", newWebTarget("", endpoint.URL, endpoint.HostHeader)))
		}
	}

	if len(d.sprayResults) > 0 {
		sb.WriteString("\n## Web Evidence\n\n")
		for _, item := range sortedCopy(d.sprayResults, func(a, b sprayObservation) bool {
			return sprayResultSortKey(a) < sprayResultSortKey(b)
		}) {
			if item.Result == nil {
				continue
			}
			writeMarkdownEventLine(&sb, targetEvent(item.Capability, "", newWebProbeTarget("", item.Capability, "", item.Result)))
		}
	}

	if len(d.fingerprints) > 0 {
		sb.WriteString("\n## Fingerprints\n\n")
		for _, finger := range sortedCopy(d.fingerprints, func(a, b fingerprint) bool {
			if a.Target == b.Target {
				return a.Name < b.Name
			}
			return a.Target < b.Target
		}) {
			writeMarkdownEventLine(&sb, findingEvent(finger.Source, fingerprintFinding{
				Target:  finger.Target,
				Fingers: []string{finger.Name},
				Focus:   finger.Focus,
			}))
		}
	}

	if len(d.zombieResults) > 0 {
		sb.WriteString("\n## Risks\n\n")
		for _, result := range d.zombieResults {
			finding := weakpassFinding{Result: result}
			status := d.verificationStatus(finding.Kind(), finding.Key())
			line := formatEventLine(findingEvent(capZombieWeakpass, finding), false)
			writeMarkdownStatusLine(&sb, line, status)
		}
	}

	if len(d.neutronMatches) > 0 {
		sb.WriteString("\n## Vulnerabilities\n\n")
		for _, finding := range sortedCopy(d.neutronMatches, func(a, b vulnFinding) bool {
			return a.String() < b.String()
		}) {
			status := d.verificationStatus(finding.Kind(), finding.Key())
			line := formatEventLine(findingEvent(capNeutronPOC, finding), false)
			writeMarkdownStatusLine(&sb, line, status)
		}
	}

	if len(d.verifications) > 0 {
		sb.WriteString("\n## AI Review\n\n")
		for _, item := range sortedCopy(d.verifications, func(a, b verificationResult) bool {
			left := a.Finding
			right := b.Finding
			return string(left.Status)+"|"+left.Target+"|"+left.OriginalKey < string(right.Status)+"|"+right.Target+"|"+right.OriginalKey
		}) {
			writeMarkdownEventLine(&sb, findingEvent(item.Source, item.Finding))
		}
	}

	if len(d.aiSkillResults) > 0 {
		sb.WriteString("\n## AI Skill Findings\n\n")
		for _, item := range sortedCopy(d.aiSkillResults, func(a, b aiSkillResult) bool {
			return a.Finding.Skill+"|"+a.Finding.Target < b.Finding.Skill+"|"+b.Finding.Target
		}) {
			line := formatEventLine(findingEvent(item.Source, item.Finding), false)
			if line == "" {
				continue
			}
			writeMarkdownStatusLine(&sb, line, d.aiSkillReportStatus(item.Finding))
		}
	}

	if len(d.errors) > 0 {
		sb.WriteString("\n## Errors\n\n")
		for _, line := range sortedCopy(d.errors, func(a, b string) bool { return a < b }) {
			writeMarkdownEventLine(&sb, errorEventOf("scan", line))
		}
	}

	if d.debug && len(d.trace) > 0 {
		sb.WriteString("\n## Trace\n\n")
		for _, line := range d.trace {
			sb.WriteString("- ")
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

func formatJSONLines(d *collector) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var sb strings.Builder
	for _, result := range d.gogoResults {
		line, err := json.Marshal(result)
		if err != nil {
			return "", err
		}
		sb.Write(line)
		sb.WriteByte('\n')
	}
	for _, item := range d.sprayResults {
		line, err := json.Marshal(item.Result)
		if err != nil {
			return "", err
		}
		sb.Write(line)
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}

func formatPlainText(d *collector, fileLines []string) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	var sb strings.Builder
	for _, line := range fileLines {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	sb.WriteString(formatScanSummaryLine(d, d.statsSnapshotLocked(), false))
	return sb.String()
}

func formatScanSummaryLine(d *collector, stats statsSnapshot, color bool) string {
	parts := []string{"completed"}
	parts = appendCount(parts, stats.Inputs, "target", "targets")
	parts = appendCount(parts, len(d.gogoResults), "service", "services")
	parts = appendCount(parts, len(d.webEndpoints), "web", "web")
	parts = appendCount(parts, len(d.sprayResults), "probe", "probes")
	parts = appendCount(parts, len(d.fingerprints), "fingerprint", "fingerprints")
	parts = appendCount(parts, len(d.zombieResults), "risk", "risks")
	parts = appendCount(parts, len(d.neutronMatches), "vuln", "vulns")
	parts = appendCount(parts, d.confirmedVerificationCountLocked(), "verified", "verified")
	parts = appendCount(parts, len(d.errors), "error", "errors")
	parts = appendCount64(parts, stats.Tasks, "task", "tasks")
	parts = appendCount64(parts, stats.Requests, "request", "requests")
	parts = append(parts, stats.Duration().Round(time.Millisecond).String())
	output := strings.Join(parts, " ")
	return formatOutputLine(outputPrefix("summary", ansiDim, color), output, color) + "\n"
}

func appendCount(parts []string, n int, singular, plural string) []string {
	word := plural
	if n == 1 {
		word = singular
	}
	return append(parts, strconv.Itoa(n), word)
}

func appendCount64(parts []string, n int64, singular, plural string) []string {
	word := plural
	if n == 1 {
		word = singular
	}
	return append(parts, strconv.FormatInt(n, 10), word)
}

func sortedCopy[T any](items []T, less func(a, b T) bool) []T {
	out := append([]T(nil), items...)
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

func sprayResultSortKey(item sprayObservation) string {
	if item.Result == nil {
		return item.Capability
	}
	return item.Result.UrlString + "|" + item.Capability + "|" + item.Result.Source.Name()
}

func formatTraceEvent(event pipelineEvent) string {
	parts := []string{string(event.Action)}
	if event.Capability != "" {
		parts = append(parts, event.Capability)
	}
	parts = append(parts, string(event.Event.label()))
	if event.Event.Source != "" {
		parts = append(parts, event.Event.Source)
	}
	targetValue := ""
	hostHeader := ""
	switch target := event.Event.Target.(type) {
	case scanTarget:
		if target.Target != "" {
			targetValue = target.Target
		}
	case serviceTarget:
		if target.Result != nil {
			targetValue = target.Result.GetTarget()
		}
	case webTarget:
		if target.URL != "" {
			targetValue = target.URL
		}
		hostHeader = target.HostHeader
	case webProbeTarget:
		if target.Result != nil && target.Result.UrlString != "" {
			targetValue = target.Result.UrlString
		}
		hostHeader = target.HostHeader
	case pocTarget:
		if target.Target != "" {
			targetValue = target.Target
		}
	case weakpassTarget:
		if target.Target.Address() != ":" {
			targetValue = target.Target.Address()
		}
	}
	if targetValue != "" {
		parts = append(parts, targetValue)
	}
	if hostHeader != "" {
		parts = append(parts, hostHeader)
	}
	if event.Event.Kind == eventError && event.Event.Error.Message != "" {
		parts = append(parts, event.Event.Error.Message)
	}
	return formatOutputLine("[trace]", parsers.JoinOutput(parts...), false)
}

func writeMarkdownEventLine(sb *strings.Builder, event event) {
	line := formatEventLine(event, false)
	if line == "" {
		return
	}
	writeMarkdownStatusLine(sb, line, "")
}

func writeMarkdownStatusLine(sb *strings.Builder, line, status string) {
	if line == "" {
		return
	}
	sb.WriteString("- ")
	switch status {
	case string(verificationNotConfirmed):
		sb.WriteString("~~")
		sb.WriteString(line)
		sb.WriteString("~~ *(not confirmed)*")
	case string(verificationConfirmed):
		sb.WriteString("**[verified]** ")
		sb.WriteString(line)
	case string(verificationInconclusive):
		sb.WriteString("**[inconclusive]** ")
		sb.WriteString(line)
	case string(verificationFailed):
		sb.WriteString("**[verification failed]** ")
		sb.WriteString(line)
	default:
		sb.WriteString(line)
	}
	sb.WriteString("\n")
}

func (d *collector) aiSkillReportStatus(finding aiSkillFinding) string {
	status := strings.TrimSpace(finding.Status)
	if status == string(verificationNotConfirmed) || status == string(verificationInconclusive) {
		return status
	}
	if finding.OriginalKey != "" {
		status = verificationDowngrade(status, d.verificationStatus(finding.OriginalKind, finding.OriginalKey))
	}
	return verificationDowngrade(status, d.verificationStatus(finding.Kind(), finding.Key()))
}

func verificationDowngrade(current, verified string) string {
	switch verified {
	case string(verificationNotConfirmed), string(verificationInconclusive):
		return verified
	default:
		return current
	}
}

func sortedMapKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func writeCountTable(sb *strings.Builder, label string, values map[string]int) {
	sb.WriteString(fmt.Sprintf("| %s | Count |\n", label))
	sb.WriteString("| --- | ---: |\n")
	for _, key := range sortedMapKeys(values) {
		sb.WriteString(fmt.Sprintf("| %s | %d |\n", key, values[key]))
	}
}

func sortedStatsKeys(values map[string]sdktypes.Stats) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func writeEngineStatsTable(sb *strings.Builder, values map[string]sdktypes.Stats) {
	sb.WriteString("| Source | Engine | Task | Targets | Tasks | Requests | Results | Errors | Duration |\n")
	sb.WriteString("| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, key := range sortedStatsKeys(values) {
		stats := values[key]
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %d | %d | %d | %d | %d | %s |\n",
			key,
			stats.Engine,
			stats.Task,
			stats.Targets,
			stats.Tasks,
			stats.Requests,
			stats.Results,
			stats.Errors,
			stats.Duration.Round(time.Millisecond),
		))
	}
}
