package scan

import (
	"time"

	"github.com/chainreactors/aiscan/core/output"
)

func (c *collector) StructuredResult() *output.Result {
	c.mu.Lock()
	defer c.mu.Unlock()

	stats := c.statsSnapshotLocked()
	result := &output.Result{
		Summary: output.Summary{
			Targets:    stats.Inputs,
			Services:   len(c.gogoResults),
			Webs:       len(c.seenWeb),
			Probes:     len(c.sprayResults),
			Loots:      len(c.loots),
			Errors:     len(c.errors),
			Tasks:      stats.Tasks,
			Requests:   stats.Requests,
			Duration:   stats.Duration().Round(time.Millisecond).String(),
			StartedAt:  stats.StartedAt,
			FinishedAt: stats.FinishedAt,
		},
	}

	for _, item := range c.gogoResults {
		if item == nil {
			continue
		}
		result.Services = append(result.Services, item)
	}
	for _, item := range c.sprayResults {
		if item.Result == nil {
			continue
		}
		result.WebProbes = append(result.WebProbes, item.Result)
	}
	result.Loots = append(result.Loots, c.loots...)
	for _, message := range c.errors {
		result.Errors = append(result.Errors, output.Error{Message: message})
	}

	result.Assets = AggregateStructuredResult(result)
	result.Nodes = buildSCONodes(result)
	return result
}
