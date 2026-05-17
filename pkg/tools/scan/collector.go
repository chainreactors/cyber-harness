package scan

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/util"
	"github.com/chainreactors/parsers"
	sdkkit "github.com/chainreactors/sdk/pkg"
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
	Focus  bool
}

type sprayObservation struct {
	Result     *parsers.SprayResult
	Capability string
}

type verificationResult struct {
	Finding verificationFinding
	Source  string
}

type aiSkillResult struct {
	Finding aiSkillFinding
	Source  string
}

type collector struct {
	mu             sync.Mutex
	inputs         []string
	debug          bool
	stats          *statsCollector
	recorder       *recorder
	webEndpoints   []webEndpoint
	gogoResults    []*parsers.GOGOResult
	sprayResults   []sprayObservation
	fingerprints   []fingerprint
	zombieResults  []*parsers.ZombieResult
	neutronMatches []vulnFinding
	verifications  []verificationResult
	aiSkillResults []aiSkillResult
	errors         []string
	trace          []string
	seenWeb        map[string]struct{}
	seenFinger     map[string]int
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
		seenFinger:  make(map[string]int),
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
			if c.recorder != nil {
				c.recorder.Web(target.URL, 0, "", nil)
			}
		}
	case serviceTarget:
		if target.Result != nil {
			c.gogoResults = append(c.gogoResults, target.Result)
			if c.recorder != nil {
				c.recorder.Service(target.Result)
			}
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
			if c.recorder != nil && target.Result != nil {
				c.recorder.Web(target.Result.UrlString, target.Result.Status, target.Result.Title, parsers.FrameworkNames(target.Result.Frameworks))
			}
		}
	}
}

func (c *collector) recordFindingEvent(event event) {
	switch finding := event.Finding.(type) {
	case fingerprintFinding:
		for _, name := range parsers.NormalizeNames(finding.Fingers) {
			key := strings.ToLower(finding.Target) + "|" + strings.ToLower(name)
			if index, ok := c.seenFinger[key]; ok {
				if finding.Focus && index >= 0 && index < len(c.fingerprints) && !c.fingerprints[index].Focus {
					c.fingerprints[index].Focus = true
					c.fingerprints[index].Source = event.Source
				}
				continue
			}
			c.seenFinger[key] = len(c.fingerprints)
			c.fingerprints = append(c.fingerprints, fingerprint{
				Target: finding.Target,
				Name:   name,
				Source: event.Source,
				Focus:  finding.Focus,
			})
		}
	case weakpassFinding:
		if finding.Result != nil {
			c.zombieResults = append(c.zombieResults, finding.Result)
		}
	case vulnFinding:
		if finding.String() != "" {
			c.neutronMatches = append(c.neutronMatches, finding)
		}
	case verificationFinding:
		if reportableVerificationFinding(finding) {
			c.verifications = append(c.verifications, verificationResult{
				Finding: finding,
				Source:  event.Source,
			})
		}
	case aiSkillFinding:
		if finding.Status == "confirmed" && (finding.Summary != "" || finding.Detail != "") {
			c.aiSkillResults = append(c.aiSkillResults, aiSkillResult{
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

type statsSnapshot struct {
	StartedAt         time.Time
	FinishedAt        time.Time
	Inputs            int
	Accepted          map[string]int
	CapabilityRuns    map[string]int
	CapabilityOutput  map[string]int
	SprayByCapability map[string]int
	ErrorsBySource    map[string]int
	EngineStats       map[string]sdkkit.Stats
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
			EngineStats:       make(map[string]sdkkit.Stats),
		},
	}
}

func (s *statsCollector) Observe(event pipelineEvent) {
	switch event.Action {
	case pipelineEventAccept:
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
	case pipelineEventCapabilityStart:
		s.summary.CapabilityRuns[event.Capability]++
	case pipelineEventEmit:
		if event.Event.Source != "" {
			s.summary.CapabilityOutput[event.Event.Source]++
		}
	}
}

func (s *statsCollector) recordEngineStats(source string, stats sdkkit.Stats) {
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
	out.Accepted = util.CloneMap(out.Accepted)
	out.CapabilityRuns = util.CloneMap(out.CapabilityRuns)
	out.CapabilityOutput = util.CloneMap(out.CapabilityOutput)
	out.SprayByCapability = util.CloneMap(out.SprayByCapability)
	out.ErrorsBySource = util.CloneMap(out.ErrorsBySource)
	out.EngineStats = util.CloneMap(out.EngineStats)
	return out
}

func (s statsSnapshot) Duration() time.Duration {
	finished := s.FinishedAt
	if finished.IsZero() {
		finished = time.Now()
	}
	return finished.Sub(s.StartedAt)
}
