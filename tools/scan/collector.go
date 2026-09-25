package scan

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/cyber/tools/scan/pipeline"
	"github.com/chainreactors/utils/parsers"
)

type collector struct {
	mu           sync.Mutex
	inputs       int
	debug        bool
	startedAt    time.Time
	finishedAt   time.Time
	tasks        int64
	requests     int64
	gogoResults  []*parsers.GOGOResult
	sprayResults []*parsers.SprayResult
	artifacts    []artifactResult
	loots        []parsers.Loot
	errors       []string
	canceled     bool
	trace        []string
	seenWeb      map[string]struct{}
	seenFinger   map[string]struct{}
	stream       io.Writer
	streamColor  bool
	fileLines    []string
}

func newCollector(inputs []string, stream io.Writer, streamColor, debug bool) *collector {
	return &collector{
		inputs:      len(inputs),
		debug:       debug,
		startedAt:   time.Now(),
		seenWeb:     make(map[string]struct{}),
		seenFinger:  make(map[string]struct{}),
		stream:      stream,
		streamColor: streamColor,
	}
}

func (c *collector) Observe(pe pipeline.Observation[event]) {
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
	if accepted && pe.Event.Kind == eventStats && (pe.Event.Stats.Engine != "" || pe.Event.Stats.Task != "") {
		c.tasks += pe.Event.Stats.Tasks
		c.requests += pe.Event.Stats.Requests
	}
	if accepted {
		switch pe.Event.Kind {
		case eventTarget:
			c.recordTargetEvent(pe.Event)
		case eventLoot:
			c.recordLootEvent(pe.Event)
		case eventError:
			if pe.Event.Error != "" {
				c.errors = append(c.errors, pe.Event.Error)
			}
		}
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

func (c *collector) recordTargetEvent(event event) {
	switch target := event.Target.(type) {
	case webTarget:
		c.seenWeb[target.Key()] = struct{}{}
	case serviceTarget:
		if target.Result != nil {
			c.gogoResults = append(c.gogoResults, target.Result)
		}
	case webProbeTarget:
		if reportableSprayResultForCapability(target.Result, event.Source) {
			c.sprayResults = append(c.sprayResults, target.Result)
		}
	}
}

func (c *collector) recordLootEvent(event event) {
	if event.Loot == nil {
		return
	}
	loot := *event.Loot
	if event.Artifact != nil {
		c.artifacts = append(c.artifacts, *event.Artifact)
	}
	switch loot.Kind {
	case parsers.LootFingerprint:
		fingers := loot.Tags
		for _, name := range parsers.NormalizeNames(fingers) {
			key := strings.ToLower(loot.Target) + "|" + strings.ToLower(name)
			c.seenFinger[key] = struct{}{}
		}
	}
	c.loots = append(c.loots, loot)
}

func (c *collector) Finish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.finishedAt = time.Now()
}

func (c *collector) duration() time.Duration {
	if c.startedAt.IsZero() {
		return 0
	}
	finished := c.finishedAt
	if finished.IsZero() {
		finished = time.Now()
	}
	return finished.Sub(c.startedAt)
}
