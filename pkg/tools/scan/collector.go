package scan

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/util"
	"github.com/chainreactors/parsers"
	"github.com/chainreactors/utils"
)

type webEndpoint struct {
	URL        string
	HostHeader string
	Source     string
}

type fingerprint struct {
	Target string
	Name   string
	Source string
}

type sprayObservation struct {
	Result     *parsers.SprayResult
	Capability string
}

type verificationResult struct {
	Finding verificationFinding
	Source  string
}

type collector struct {
	mu             sync.Mutex
	inputs         []string
	debug          bool
	stats          *statsCollector
	webEndpoints   []webEndpoint
	gogoResults    []*parsers.GOGOResult
	sprayResults   []sprayObservation
	fingerprints   []fingerprint
	zombieResults  []*parsers.ZombieResult
	neutronMatches []string
	verifications  []verificationResult
	errors         []string
	trace          []string
	seenWeb        map[string]struct{}
	seenFinger     map[string]struct{}
	stream         io.Writer
	streamColor    bool
	fileLines      []string
}

func newCollector(inputs []string, stream io.Writer, streamColor, debug bool) *collector {
	return &collector{
		inputs:      append([]string(nil), inputs...),
		debug:       debug,
		stats:       newStatsCollector(len(inputs)),
		seenWeb:     make(map[string]struct{}),
		seenFinger:  make(map[string]struct{}),
		stream:      stream,
		streamColor: streamColor,
		fileLines:   make([]string, 0),
	}
}

func (c *collector) Observe(pe pipelineEvent) {
	c.mu.Lock()
	if c.debug {
		c.trace = append(c.trace, formatTraceEvent(pe))
	}
	if c.stats != nil {
		c.stats.Observe(pe)
	}
	if pe.Action == pipelineEventAccept {
		c.recordAcceptedEvent(pe.Event)
	}
	c.mu.Unlock()

	if pe.Action != pipelineEventAccept {
		return
	}

	plain := formatEventLine(pe.Event, false)
	if plain == "" {
		return
	}

	c.mu.Lock()
	c.fileLines = append(c.fileLines, plain)
	c.mu.Unlock()

	if c.stream == nil {
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
	case eventFinding:
		c.recordFindingEvent(event)
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
			c.webEndpoints = append(c.webEndpoints, webEndpoint{
				URL:        target.URL,
				HostHeader: target.HostHeader,
				Source:     event.Source,
			})
		}
	case serviceTarget:
		if target.Result != nil {
			c.gogoResults = append(c.gogoResults, target.Result)
		}
	case webProbeTarget:
		if reportableSprayResult(target.Result) {
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

func (c *collector) recordFindingEvent(event event) {
	switch finding := event.Finding.(type) {
	case fingerprintFinding:
		for _, name := range parsers.NormalizeNames(finding.Fingers) {
			key := strings.ToLower(finding.Target) + "|" + strings.ToLower(name)
			if _, ok := c.seenFinger[key]; ok {
				continue
			}
			c.seenFinger[key] = struct{}{}
			c.fingerprints = append(c.fingerprints, fingerprint{
				Target: finding.Target,
				Name:   name,
				Source: event.Source,
			})
		}
	case weakpassFinding:
		if finding.Result != nil {
			c.zombieResults = append(c.zombieResults, finding.Result)
		}
	case vulnFinding:
		if finding.Message != "" {
			c.neutronMatches = append(c.neutronMatches, finding.Message)
		}
	case verificationFinding:
		if finding.Status != "" || finding.Summary != "" {
			c.verifications = append(c.verifications, verificationResult{
				Finding: finding,
				Source:  event.Source,
			})
		}
	}
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
	return formatSummary(c)
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
