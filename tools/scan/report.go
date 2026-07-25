package scan

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/utils/parsers"
	sdktypes "github.com/chainreactors/sdk/pkg/types"
)

func formatSummary(d *collector, color bool) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	stats := d.statsSnapshotLocked()

	var sb strings.Builder
	if d.stream == nil {
		for _, line := range d.fileLines {
			sb.WriteString(output.SanitizeLine(line, output.NewColor(color)))
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
	sb.WriteString(fmt.Sprintf("| Web endpoints | %d |\n", len(d.seenWeb)))
	sb.WriteString(fmt.Sprintf("| Web probes | %d |\n", len(d.sprayResults)))
	sb.WriteString(fmt.Sprintf("| Fingerprints | %d |\n", len(d.seenFinger)))
	sb.WriteString(fmt.Sprintf("| Loots | %d |\n", len(d.loots)))
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

	if len(d.loots) > 0 {
		sb.WriteString("\n## Loots\n\n")
		for _, loot := range sortedCopy(d.loots, func(a, b output.Loot) bool {
			if a.Kind != b.Kind {
				return a.Kind < b.Kind
			}
			return a.Description < b.Description
		}) {
			status, _ := loot.Data["verification_status"].(string)
			line := formatEventLine(lootEvent(loot.Kind, loot), false)
			if line != "" {
				writeMarkdownStatusLine(&sb, line, status)
			}
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

func formatScanSummaryLine(d *collector, stats statsSnapshot, color bool) string {
	parts := []string{"completed"}
	parts = appendCount(parts, stats.Inputs, "target", "targets")
	parts = appendCount(parts, len(d.gogoResults), "service", "services")
	parts = appendCount(parts, len(d.seenWeb), "web", "web")
	parts = appendCount(parts, len(d.sprayResults), "probe", "probes")
	parts = appendCount(parts, len(d.seenFinger), "fingerprint", "fingerprints")
	parts = appendCount(parts, len(d.loots), "loot", "loots")
	parts = appendCount(parts, len(d.errors), "error", "errors")
	parts = appendCount64(parts, stats.Tasks, "task", "tasks")
	parts = appendCount64(parts, stats.Requests, "request", "requests")
	parts = append(parts, stats.Duration().Round(time.Millisecond).String())
	c := output.NewColor(color)
	body := strings.Join(parts, " ")
	return output.FormatLine(output.OutputPrefix("summary", c.Dim), body, c) + "\n"
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
	return output.FormatLine("[trace]", parsers.JoinOutput(parts...), output.NewColor(false))
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
	case "not_confirmed":
		sb.WriteString("~~")
		sb.WriteString(line)
		sb.WriteString("~~ *(not confirmed)*")
	case "confirmed":
		sb.WriteString("**[verified]** ")
		sb.WriteString(line)
	case "inconclusive":
		sb.WriteString("**[inconclusive]** ")
		sb.WriteString(line)
	case "failed":
		sb.WriteString("**[verification failed]** ")
		sb.WriteString(line)
	default:
		sb.WriteString(line)
	}
	sb.WriteString("\n")
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
