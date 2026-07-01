package output

import (
	"time"

	"github.com/chainreactors/utils/parsers"
)

type Result struct {
	Summary   Summary                `json:"summary"`
	Assets    []Asset                `json:"assets,omitempty"`
	Services  []*parsers.GOGOResult  `json:"services,omitempty"`
	WebProbes []*parsers.SprayResult `json:"web_probes,omitempty"`
	Loots     []Loot                 `json:"loots,omitempty"`
	Errors    []Error                `json:"errors,omitempty"`
}

type Summary struct {
	Targets    int       `json:"targets"`
	Services   int       `json:"services"`
	Webs       int       `json:"webs"`
	Probes     int       `json:"probes"`
	Loots      int       `json:"loots"`
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

type Asset struct {
	ID     string      `json:"id"`
	Key    string      `json:"key"`
	Target string      `json:"target"`
	Title  string      `json:"title,omitempty"`
	Status string      `json:"status,omitempty"`
	Items  []AssetItem `json:"items,omitempty"`
}

const (
	AssetItemService     = "service"
	AssetItemPath        = "path"
	AssetItemFingerprint = "fingerprint"
	AssetItemLoot        = "loot"
	AssetItemNote        = "note"
	AssetItemResponse    = "response"
	AssetItemError       = "error"
)

type AssetItem struct {
	Kind    string         `json:"kind"`
	Source  string         `json:"source,omitempty"`
	Target  string         `json:"target,omitempty"`
	Status  string         `json:"status,omitempty"`
	Title   string         `json:"title,omitempty"`
	Summary string         `json:"summary,omitempty"`
	Detail  string         `json:"detail,omitempty"`
	Tags    []string       `json:"tags,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
	Raw     string         `json:"raw,omitempty"`
}

type Error struct {
	Source  string `json:"source,omitempty"`
	Message string `json:"message"`
}

// --- Record payload types (aiscan-specific) ---

type ScanStart struct {
	Targets []string `json:"targets"`
	Mode    string   `json:"mode"`
	Flags   []string `json:"flags"`
}

type ScanEnd struct {
	Duration float64 `json:"duration_s"`
	Targets  int     `json:"targets"`
	Services int     `json:"services"`
	Webs     int     `json:"webs"`
	Loots    int     `json:"loots"`
	Errors   int     `json:"errors"`
}
