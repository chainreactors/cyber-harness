package scan

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/tools/scan/pipeline"
	"github.com/chainreactors/utils/parsers"
	sdktypes "github.com/chainreactors/sdk/pkg/types"
	"github.com/chainreactors/utils"
)

type sprayObservation struct {
	Result     *parsers.SprayResult
	Capability string
}

type collector struct {
	mu           sync.Mutex
	inputs       []string
	debug        bool
	stats        *statsCollector
	gogoResults  []*parsers.GOGOResult
	sprayResults []sprayObservation
	loots        []output.Loot
	errors       []string
	trace        []string
	seenWeb      map[string]struct{}
	seenFinger   map[string]int
	stream       io.Writer
	streamColor  bool
	fileLines    []string
}

func newCollector(inputs []string, stream io.Writer, streamColor, debug bool) *collector {
	return &collector{
		inputs:      append([]string(nil), inputs...),
		debug:       debug,
		stats:       newStatsCollector(len(inputs)),
		seenWeb:     make(map[string]struct{}),
		seenFinger:  make(map[string]int),
		stream:      stream,
		streamColor: streamColor,
		fileLines:   make([]string, 0),
	}
}

func (c *collector) Observe(pe pipelineEvent) {
	accepted := pe.Action == pipeline.ActionAccept

	var traceEntry string
	if c.debug {
		traceEntry = formatTraceEvent(pe)
	}
	var plain string
	if accepted {
		plain = formatEventLine(pe.Event, false)
	}

	c.mu.Lock()
	if traceEntry != "" {
		c.trace = append(c.trace, traceEntry)
	}
	if c.stats != nil {
		c.stats.Observe(pe)
	}
	if accepted {
		c.recordAcceptedEvent(pe.Event)
		if plain != "" {
			c.fileLines = append(c.fileLines, plain)
		}
	}
	c.mu.Unlock()

	if !accepted || c.stream == nil {
		return
	}
	line := formatEventLine(pe.Event, c.streamColor)
	if line != "" {
		fmt.Fprintln(c.stream, line)
	}
}

func (c *collector) recordAcceptedEvent(event event) {
	switch event.Kind {
	case eventTarget:
		c.recordTargetEvent(event)
	case eventLoot:
		c.recordLootEvent(event)
	case eventError:
		if event.Error.Message != "" {
			c.errors = append(c.errors, event.Error.Message)
		}
	}
}

func (c *collector) recordTargetEvent(event event) {
	switch target := event.Target.(type) {
	case webTarget:
		key := utils.NormalizeURL(target.URL) + "|host=" + strings.ToLower(target.HostHeader)
		if _, ok := c.seenWeb[key]; !ok {
			c.seenWeb[key] = struct{}{}
		}
	case serviceTarget:
		if target.Result != nil {
			c.gogoResults = append(c.gogoResults, target.Result)
		}
	case webProbeTarget:
		if reportableSprayResultForCapability(target.Result, target.Capability) {
			source := target.Capability
			if source == "" {
				source = event.Source
			}
			c.sprayResults = append(c.sprayResults, sprayObservation{
				Result:     target.Result,
				Capability: source,
			})
		}
	}
}

func (c *collector) recordLootEvent(event event) {
	if event.Loot == nil {
		return
	}
	loot := *event.Loot
	switch loot.Kind {
	case output.LootFingerprint:
		fingers := loot.Tags
		for _, name := range parsers.NormalizeNames(fingers) {
			key := strings.ToLower(loot.Target) + "|" + strings.ToLower(name)
			if _, ok := c.seenFinger[key]; ok {
				continue
			}
			c.seenFinger[key] = len(c.seenFinger)
		}
	}
	c.loots = append(c.loots, loot)
}

func (c *collector) Finish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stats != nil {
		c.stats.Finish()
	}
}

func (c *collector) statsSnapshotLocked() statsSnapshot {
	if c.stats != nil {
		return c.stats.Snapshot()
	}
	stats := newStatsCollector(len(c.inputs))
	stats.Finish()
	return stats.Snapshot()
}

func (c *collector) String() string {
	return formatSummary(c, false)
}

func (c *collector) TerminalString(color bool) string {
	return formatSummary(c, color)
}

func (c *collector) ReportMarkdown() string {
	return formatMarkdown(c)
}

func (c *collector) JSONLines() (string, error) {
	return formatJSONLines(c)
}

func (c *collector) PlainText() string {
	c.mu.Lock()
	lines := append([]string(nil), c.fileLines...)
	c.mu.Unlock()
	return formatPlainText(c, lines)
}

func (c *collector) AssetReport() string {
	return output.FormatAssetReport(c.StructuredResult(), false)
}

func (c *collector) LootReport() string {
	return c.AssetReport()
}

type statsSnapshot struct {
	StartedAt         time.Time
	FinishedAt        time.Time
	Inputs            int
	Accepted          map[string]int
	CapabilityRuns    map[string]int
	CapabilityOutput  map[string]int
	SprayByCapability map[string]int
	ErrorsBySource    map[string]int
	EngineStats       map[string]sdktypes.Stats
	Tasks             int64
	Requests          int64
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
			EngineStats:       make(map[string]sdktypes.Stats),
		},
	}
}

func (s *statsCollector) Observe(event pipelineEvent) {
	switch event.Action {
	case pipeline.ActionAccept:
		if event.Event.Kind == eventStats {
			s.recordEngineStats(event.Event.Source, event.Event.Stats)
			return
		}
		s.summary.Accepted[event.Event.label()]++
		if event.Event.Kind == eventError && event.Event.Error.Message != "" {
			s.summary.ErrorsBySource[event.Event.Source]++
		}
		if target, ok := event.Event.Target.(webProbeTarget); ok && reportableSprayResultForCapability(target.Result, target.Capability) {
			source := target.Capability
			if source == "" {
				source = event.Event.Source
			}
			s.summary.SprayByCapability[source]++
		}
	case pipeline.ActionCapabilityStart:
		s.summary.CapabilityRuns[event.Capability]++
	case pipeline.ActionEmit:
		if event.Event.Source != "" {
			s.summary.CapabilityOutput[event.Event.Source]++
		}
	}
}

func (s *statsCollector) recordEngineStats(source string, stats sdktypes.Stats) {
	if stats.Engine == "" && stats.Task == "" {
		return
	}
	s.summary.Tasks += stats.Tasks
	s.summary.Requests += stats.Requests

	key := source
	if key == "" {
		key = stats.Engine
	}
	if key == "" {
		key = stats.Task
	}
	current := s.summary.EngineStats[key]
	if current.Engine == "" {
		current.Engine = stats.Engine
	}
	if current.Task == "" {
		current.Task = stats.Task
	}
	current.Targets += stats.Targets
	current.Tasks += stats.Tasks
	current.Requests += stats.Requests
	current.Results += stats.Results
	current.Errors += stats.Errors
	current.Duration += stats.Duration
	s.summary.EngineStats[key] = current
}

func (s *statsCollector) Finish() {
	s.summary.FinishedAt = time.Now()
}

func (s *statsCollector) Snapshot() statsSnapshot {
	out := s.summary
	out.Accepted = cloneMap(out.Accepted)
	out.CapabilityRuns = cloneMap(out.CapabilityRuns)
	out.CapabilityOutput = cloneMap(out.CapabilityOutput)
	out.SprayByCapability = cloneMap(out.SprayByCapability)
	out.ErrorsBySource = cloneMap(out.ErrorsBySource)
	out.EngineStats = cloneMap(out.EngineStats)
	return out
}

func (s statsSnapshot) Duration() time.Duration {
	finished := s.FinishedAt
	if finished.IsZero() {
		finished = time.Now()
	}
	return finished.Sub(s.StartedAt)
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return nil
	}
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
