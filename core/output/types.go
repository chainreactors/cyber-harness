package output

import (
	"time"

	"github.com/chainreactors/utils/parsers"
)

// ScanResult is private collector state. Scanner-native records leave a node
// only as ToolArtifact messages; the server owns normalization and persistence.
type ScanResult struct {
	Summary Summary                `json:"summary"`
	GOGO    []*parsers.GOGOResult  `json:"gogo,omitempty"`
	Spray   []*parsers.SprayResult `json:"spray,omitempty"`
	Loots   []Loot                 `json:"loots,omitempty"`
	Errors  []Error                `json:"errors,omitempty"`
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
