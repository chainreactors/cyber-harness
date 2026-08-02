package scan

import (
	"time"

	"github.com/chainreactors/aiscan/core/output"
)

func (c *collector) StructuredResult() *output.ScanResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	stats := c.statsSnapshotLocked()
	result := &output.ScanResult{
		Summary: output.Summary{
			Inputs:     stats.Inputs,
			Ports:      len(c.gogoResults),
			Web:        len(c.seenWeb),
			URLs:       len(c.sprayResults),
			Findings:   len(c.loots),
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
		result.GOGO = append(result.GOGO, item)
	}
	for _, item := range c.sprayResults {
		if item.Result == nil {
			continue
		}
		result.Spray = append(result.Spray, item.Result)
	}
	result.Loots = append(result.Loots, c.loots...)
	for _, message := range c.errors {
		result.Errors = append(result.Errors, output.Error{Message: message})
	}

	return result
}
