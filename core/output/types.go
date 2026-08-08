package output

import (
	"time"

	"github.com/chainreactors/utils/parsers"
)

// ScanResult is private collector state. Scanner-native records leave a node
// only as canonical aop.tool.Artifact messages.
type ScanResult struct {
	Summary   Summary                `json:"summary"`
	GOGO      []*parsers.GOGOResult  `json:"gogo,omitempty"`
	Spray     []*parsers.SprayResult `json:"spray,omitempty"`
	Artifacts []ArtifactResult       `json:"artifacts,omitempty"`
	Loots     []Loot                 `json:"loots,omitempty"`
	Errors    []Error                `json:"errors,omitempty"`
}

// ArtifactResult keeps the scanner-native result paired with a Loot marker.
// Data is serialized directly into aop.tool.Artifact without reshaping.
type ArtifactResult struct {
	ResultID string `json:"result_id"`
	Tool     string `json:"tool"`
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Data     any    `json:"data"`
}

type Summary struct {
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

type Loot = parsers.Loot

const (
	LootFingerprint = parsers.LootFingerprint
	LootWeakpass    = parsers.LootWeakpass
	LootVuln        = parsers.LootVuln
)

type Error struct {
	Source  string `json:"source,omitempty"`
	Message string `json:"message"`
}
