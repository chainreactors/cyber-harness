package scan

import (
	"strconv"
	"strings"
	"time"

	"github.com/chainreactors/cyber/pkg/output"
	"github.com/chainreactors/cyber/tools/scan/pipeline"
	"github.com/chainreactors/utils/parsers"
)

func formatSummary(d *collector, color bool) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	var sb strings.Builder
	if d.stream == nil {
		for _, line := range d.fileLines {
			sb.WriteString(output.SanitizeLine(line, output.NewColor(color)))
			sb.WriteString("\n")
		}
	}
	sb.WriteString(formatScanSummaryLine(d, color))
	for _, line := range d.trace {
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	return sb.String()
}

func formatScanSummaryLine(d *collector, color bool) string {
	status := "completed"
	if len(d.errors) > 0 {
		status = "failed"
	}
	if d.canceled {
		status = "canceled"
	}
	parts := []string{status}
	parts = appendCount(parts, d.inputs, "target", "targets")
	parts = appendCount(parts, len(d.gogoResults), "service", "services")
	parts = appendCount(parts, len(d.seenWeb), "web", "web")
	parts = appendCount(parts, len(d.sprayResults), "probe", "probes")
	parts = appendCount(parts, len(d.seenFinger), "fingerprint", "fingerprints")
	parts = appendCount(parts, len(d.loots), "loot", "loots")
	parts = appendCount(parts, len(d.errors), "error", "errors")
	parts = appendCount64(parts, d.tasks, "task", "tasks")
	parts = appendCount64(parts, d.requests, "request", "requests")
	parts = append(parts, d.duration().Round(time.Millisecond).String())
	c := output.NewColor(color)
	return output.FormatLine(output.OutputPrefix("summary", c.Dim), strings.Join(parts, " "), c) + "\n"
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

func formatTraceEvent(event pipeline.Observation[event]) string {
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
		targetValue = target.Target
	case serviceTarget:
		if target.Result != nil {
			targetValue = target.Result.GetTarget()
		}
	case webTarget:
		targetValue = target.URL
		hostHeader = target.HostHeader
	case webProbeTarget:
		if target.Result != nil {
			targetValue = target.Result.UrlString
		}
		hostHeader = target.HostHeader
	case pocTarget:
		targetValue = target.Target
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
	if event.Event.Kind == eventError && event.Event.Error != "" {
		parts = append(parts, event.Event.Error)
	}
	return output.FormatLine("[trace]", parsers.JoinOutput(parts...), output.NewColor(false))
}
