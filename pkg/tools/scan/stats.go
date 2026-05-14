package scan

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/util"
)

type statsSnapshot struct {
	StartedAt         time.Time
	FinishedAt        time.Time
	Inputs            int
	Accepted          map[string]int
	CapabilityRuns    map[string]int
	CapabilityOutput  map[string]int
	SprayByCapability map[string]int
	ErrorsBySource    map[string]int
}

type statsCollector struct {
	summary statsSnapshot
}

func newStatsCollector(inputs int) *statsCollector {
	return &statsCollector{
		summary: statsSnapshot{
			StartedAt:         time.Now(),
			Inputs:            inputs,
			Accepted:          make(map[string]int),
			CapabilityRuns:    make(map[string]int),
			CapabilityOutput:  make(map[string]int),
			SprayByCapability: make(map[string]int),
			ErrorsBySource:    make(map[string]int),
		},
	}
}

func (s *statsCollector) Observe(event pipelineEvent) {
	switch event.Action {
	case pipelineEventAccept:
		s.summary.Accepted[event.Event.label()]++
		if event.Event.Kind == eventError && event.Event.Error.Message != "" {
			s.summary.ErrorsBySource[event.Event.Source]++
		}
		if target, ok := event.Event.Target.(webProbeTarget); ok && reportableSprayResult(target.Result) {
			source := target.Capability
			if source == "" {
				source = event.Event.Source
			}
			s.summary.SprayByCapability[source]++
		}
	case pipelineEventCapabilityStart:
		s.summary.CapabilityRuns[event.Capability]++
	case pipelineEventEmit:
		if event.Event.Source != "" {
			s.summary.CapabilityOutput[event.Event.Source]++
		}
	}
}

func (s *statsCollector) Finish() {
	s.summary.FinishedAt = time.Now()
}

func (s *statsCollector) Snapshot() statsSnapshot {
	out := s.summary
	out.Accepted = util.CloneMap(out.Accepted)
	out.CapabilityRuns = util.CloneMap(out.CapabilityRuns)
	out.CapabilityOutput = util.CloneMap(out.CapabilityOutput)
	out.SprayByCapability = util.CloneMap(out.SprayByCapability)
	out.ErrorsBySource = util.CloneMap(out.ErrorsBySource)
	return out
}

func (s statsSnapshot) Duration() time.Duration {
	finished := s.FinishedAt
	if finished.IsZero() {
		finished = time.Now()
	}
	return finished.Sub(s.StartedAt)
}



func metricLine(name string, values map[string]int) string {
	return fmt.Sprintf("[scan] metrics %s %s\n", name, joinCounts(values))
}

func joinCounts(values map[string]int) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key, fmt.Sprintf("%d", values[key]))
	}
	return strings.Join(parts, " ")
}

func writeCountTable(sb *strings.Builder, label string, values map[string]int) {
	sb.WriteString(fmt.Sprintf("| %s | Count |\n", label))
	sb.WriteString("| --- | ---: |\n")
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		sb.WriteString(fmt.Sprintf("| %s | %d |\n", key, values[key]))
	}
}
