package scan

import (
	"strings"
	"sync"

	"github.com/chainreactors/parsers"
	"github.com/chainreactors/utils"
)

type scanData struct {
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
}

func newScanData(inputs []string, debug bool) *scanData {
	return &scanData{
		inputs:     append([]string(nil), inputs...),
		debug:      debug,
		stats:      newStatsCollector(len(inputs)),
		seenWeb:    make(map[string]struct{}),
		seenFinger: make(map[string]struct{}),
	}
}

func (d *scanData) Record(pe pipelineEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.debug {
		d.trace = append(d.trace, formatTraceEvent(pe))
	}
	if d.stats != nil {
		d.stats.Observe(pe)
	}
	if pe.Action == pipelineEventAccept {
		d.recordAcceptedEvent(pe.Event)
	}
}

func (d *scanData) recordAcceptedEvent(event event) {
	switch event.Kind {
	case eventTarget:
		d.recordTargetEvent(event)
	case eventFinding:
		d.recordFindingEvent(event)
	case eventError:
		if event.Error.Message != "" {
			d.errors = append(d.errors, event.Error.Message)
		}
	}
}

func (d *scanData) recordTargetEvent(event event) {
	switch target := event.Target.(type) {
	case webTarget:
		key := utils.NormalizeURL(target.URL) + "|host=" + strings.ToLower(target.HostHeader)
		if _, ok := d.seenWeb[key]; !ok {
			d.seenWeb[key] = struct{}{}
			d.webEndpoints = append(d.webEndpoints, webEndpoint{
				URL:        target.URL,
				HostHeader: target.HostHeader,
				Source:     event.Source,
			})
		}
	case serviceTarget:
		if target.Result != nil {
			d.gogoResults = append(d.gogoResults, target.Result)
		}
	case webProbeTarget:
		if reportableSprayResult(target.Result) {
			source := target.Capability
			if source == "" {
				source = event.Source
			}
			d.sprayResults = append(d.sprayResults, sprayObservation{
				Result:     target.Result,
				Capability: source,
			})
		}
	}
}

func (d *scanData) recordFindingEvent(event event) {
	switch finding := event.Finding.(type) {
	case fingerprintFinding:
		for _, name := range parsers.NormalizeNames(finding.Fingers) {
			key := strings.ToLower(finding.Target) + "|" + strings.ToLower(name)
			if _, ok := d.seenFinger[key]; ok {
				continue
			}
			d.seenFinger[key] = struct{}{}
			d.fingerprints = append(d.fingerprints, fingerprint{
				Target: finding.Target,
				Name:   name,
				Source: event.Source,
			})
		}
	case weakpassFinding:
		if finding.Result != nil {
			d.zombieResults = append(d.zombieResults, finding.Result)
		}
	case vulnFinding:
		if finding.Message != "" {
			d.neutronMatches = append(d.neutronMatches, finding.Message)
		}
	case verificationFinding:
		if finding.Status != "" || finding.Summary != "" {
			d.verifications = append(d.verifications, verificationResult{
				Finding: finding,
				Source:  event.Source,
			})
		}
	}
}

func (d *scanData) Finish() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stats != nil {
		d.stats.Finish()
	}
}

func (d *scanData) statsSnapshotLocked() statsSnapshot {
	if d.stats != nil {
		return d.stats.Snapshot()
	}
	stats := newStatsCollector(len(d.inputs))
	stats.Finish()
	return stats.Snapshot()
}
