package scan

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chainreactors/parsers"
)

func formatSummary(d *collector) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	stats := d.statsSnapshotLocked()

	var sb strings.Builder
	sb.WriteString(summaryLine(d, stats))

	if len(stats.CapabilityRuns) > 0 {
		sb.WriteString("\n")
		sb.WriteString(metricLine("runs", stats.CapabilityRuns))
	}
	if len(stats.SprayByCapability) > 0 {
		sb.WriteString("\n")
		sb.WriteString(metricLine("spray", stats.SprayByCapability))
	}
	if len(stats.ErrorsBySource) > 0 {
		sb.WriteString("\n")
		sb.WriteString(metricLine("errors", stats.ErrorsBySource))
	}

	if len(d.trace) > 0 {
		sb.WriteString("\n## Pipeline Trace\n")
		for _, line := range d.trace {
			sb.WriteString("- ")
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
	sb.WriteString(summaryLine(d, stats))
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
	sb.WriteString(fmt.Sprintf("| AI verifications | %d |\n", len(d.verifications)))
	sb.WriteString(fmt.Sprintf("| Errors | %d |\n", len(d.errors)))
	sb.WriteString(fmt.Sprintf("| Duration | %s |\n", stats.Duration().Round(time.Millisecond)))

	if len(stats.CapabilityRuns) > 0 {
		sb.WriteString("\n## Capability Runs\n\n")
		writeCountTable(&sb, "Capability", stats.CapabilityRuns)
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
		sb.WriteString("\n## Web Probe Results\n\n")
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
			}))
		}
	}

	if len(d.zombieResults) > 0 {
		sb.WriteString("\n## Weakpass Findings\n\n")
		for _, result := range d.zombieResults {
			writeMarkdownEventLine(&sb, findingEvent(capZombieWeakpass, weakpassFinding{Result: result}))
		}
	}

	if len(d.neutronMatches) > 0 {
		sb.WriteString("\n## Vulnerability Findings\n\n")
		for _, line := range sortedCopy(d.neutronMatches, func(a, b string) bool { return a < b }) {
			writeMarkdownEventLine(&sb, findingEvent(capNeutronPOC, vulnFinding{Message: line}))
		}
	}

	if len(d.verifications) > 0 {
		sb.WriteString("\n## AI Verification Results\n\n")
		for _, item := range sortedCopy(d.verifications, func(a, b verificationResult) bool {
			left := a.Finding
			right := b.Finding
			return string(left.Status)+"|"+left.Target+"|"+left.OriginalKey < string(right.Status)+"|"+right.Target+"|"+right.OriginalKey
		}) {
			writeMarkdownEventLine(&sb, findingEvent(item.Source, item.Finding))
		}
	}

	if len(d.errors) > 0 {
		sb.WriteString("\n## Errors\n\n")
		for _, line := range sortedCopy(d.errors, func(a, b string) bool { return a < b }) {
			writeMarkdownEventLine(&sb, errorEventOf("scan", line))
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
	sb.WriteString(summaryLine(d, d.statsSnapshotLocked()))
	return sb.String()
}

func summaryLine(d *collector, stats statsSnapshot) string {
	return fmt.Sprintf("[scan] completed %d %d %d %d %d %d %d %d %d %s\n",
		stats.Inputs,
		len(d.gogoResults),
		len(d.webEndpoints),
		len(d.sprayResults),
		len(d.fingerprints),
		len(d.zombieResults),
		len(d.neutronMatches),
		len(d.verifications),
		len(d.errors),
		stats.Duration().Round(time.Millisecond))
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
	parts := []string{"[scan:debug]", string(event.Action)}
	if event.Capability != "" {
		parts = append(parts, event.Capability)
	}
	parts = append(parts, string(event.Event.label()))
	if key := event.Event.Key(); key != "" {
		parts = append(parts, strconv.Quote(key))
	}
	if event.Event.Source != "" {
		parts = append(parts, event.Event.Source)
	}
	switch target := event.Event.Target.(type) {
	case scanTarget:
		if target.Target != "" {
			parts = append(parts, target.Target)
		}
	case serviceTarget:
		if target.Result != nil {
			parts = append(parts, target.Result.GetTarget())
		}
	case webTarget:
		if target.URL != "" {
			parts = append(parts, target.URL)
		}
		if target.HostHeader != "" {
			parts = append(parts, target.HostHeader)
		}
	case webProbeTarget:
		if target.Result != nil && target.Result.UrlString != "" {
			parts = append(parts, target.Result.UrlString)
		}
		if target.HostHeader != "" {
			parts = append(parts, target.HostHeader)
		}
	case pocTarget:
		if target.Target != "" {
			parts = append(parts, target.Target)
		}
	case weakpassTarget:
		if target.Target.Address() != ":" {
			parts = append(parts, target.Target.Address())
		}
	}
	if event.Event.Kind == eventError && event.Event.Error.Message != "" {
		parts = append(parts, event.Event.Error.Message)
	}
	return strings.Join(parts, " ")
}

func writeMarkdownEventLine(sb *strings.Builder, event event) {
	line := formatEventLine(event, false)
	if line == "" {
		return
	}
	sb.WriteString("- ")
	sb.WriteString(line)
	sb.WriteString("\n")
}

func metricLine(name string, values map[string]int) string {
	return fmt.Sprintf("[scan] metrics %s %s\n", name, joinCounts(values))
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

func joinCounts(values map[string]int) string {
	keys := sortedMapKeys(values)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key, fmt.Sprintf("%d", values[key]))
	}
	return strings.Join(parts, " ")
}

func writeCountTable(sb *strings.Builder, label string, values map[string]int) {
	sb.WriteString(fmt.Sprintf("| %s | Count |\n", label))
	sb.WriteString("| --- | ---: |\n")
	for _, key := range sortedMapKeys(values) {
		sb.WriteString(fmt.Sprintf("| %s | %d |\n", key, values[key]))
	}
}
