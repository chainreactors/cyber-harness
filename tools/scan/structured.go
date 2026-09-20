package scan

import (
	"time"

	"github.com/chainreactors/utils/parsers"
)

// scanResult is private collector state. Scanner-native records leave a node
// only as canonical aop.tool.Artifact messages.
type scanResult struct {
	Summary   scanSummary            `json:"summary"`
	GOGO      []*parsers.GOGOResult  `json:"gogo,omitempty"`
	Spray     []*parsers.SprayResult `json:"spray,omitempty"`
	Artifacts []artifactResult       `json:"artifacts,omitempty"`
	Loots     []parsers.Loot         `json:"loots,omitempty"`
	Errors    []scanError            `json:"errors,omitempty"`
}

// artifactResult keeps the scanner-native result paired with a Loot marker.
// Data is serialized directly into aop.tool.Artifact without reshaping.
type artifactResult struct {
	ResultID string `json:"result_id"`
	Tool     string `json:"tool"`
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Data     any    `json:"data"`
}

type scanSummary struct {
	Inputs     int       `json:"inputs"`
	Ports      int       `json:"ports"`
	Web        int       `json:"web"`
	URLs       int       `json:"urls"`
	Findings   int       `json:"findings"`
	Errors     int       `json:"errors"`
	Tasks      int64     `json:"tasks"`
	Requests   int64     `json:"requests"`
	Duration   string    `json:"duration"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

type scanError struct {
	Source  string `json:"source,omitempty"`
	Message string `json:"message"`
}

func (c *collector) StructuredResult() *scanResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	stats := c.statsSnapshotLocked()
	result := &scanResult{
		Summary: scanSummary{
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
	result.Artifacts = append(result.Artifacts, c.artifacts...)
	result.Loots = append(result.Loots, c.loots...)
	for _, message := range c.errors {
		result.Errors = append(result.Errors, scanError{Message: message})
	}

	return result
}
