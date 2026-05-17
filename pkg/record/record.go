package record

import (
	"encoding/json"
	"time"
)

type Type string

const (
	TypeScanStart  Type = "scan_start"
	TypeService    Type = "service"
	TypeWeb        Type = "web"
	TypeFinding    Type = "finding"
	TypeAISkill    Type = "ai_skill"
	TypeAITurn     Type = "ai_turn"
	TypeScanEnd    Type = "scan_end"
)

type Record struct {
	Type      Type            `json:"type"`
	Timestamp time.Time       `json:"ts"`
	Data      json.RawMessage `json:"data"`
}

type ScanStart struct {
	Targets []string `json:"targets"`
	Mode    string   `json:"mode"`
	Flags   []string `json:"flags"`
}

type Service struct {
	Target   string `json:"target"`
	Port     int    `json:"port,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Banner   string `json:"banner,omitempty"`
}

type Web struct {
	URL         string   `json:"url"`
	Status      int      `json:"status,omitempty"`
	Title       string   `json:"title,omitempty"`
	Fingers     []string `json:"fingers,omitempty"`
	ContentLen  int      `json:"content_len,omitempty"`
	Latency     string   `json:"latency,omitempty"`
}

type Finding struct {
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Priority string `json:"priority"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail,omitempty"`
}

type AISkill struct {
	Skill    string  `json:"skill"`
	Target   string  `json:"target"`
	Status   string  `json:"status"`
	Summary  string  `json:"summary"`
	Detail   string  `json:"detail,omitempty"`
	Duration float64 `json:"duration_s"`
}

type AITurn struct {
	Skill      string     `json:"skill"`
	Turn       int        `json:"turn"`
	Request    AIMessage  `json:"request"`
	Response   AIMessage  `json:"response"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	Duration   float64    `json:"duration_s"`
	Tokens     TokenUsage `json:"tokens,omitempty"`
}

type AIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Result    string `json:"result"`
	IsError   bool   `json:"is_error,omitempty"`
}

type TokenUsage struct {
	Prompt     int `json:"prompt,omitempty"`
	Completion int `json:"completion,omitempty"`
	Total      int `json:"total,omitempty"`
}

type ScanEnd struct {
	Duration    float64 `json:"duration_s"`
	Targets     int     `json:"targets"`
	Services    int     `json:"services"`
	Webs        int     `json:"webs"`
	Findings    int     `json:"findings"`
	AISkills    int     `json:"ai_skills"`
	Errors      int     `json:"errors"`
}

func New(t Type, data interface{}) Record {
	raw, _ := json.Marshal(data)
	return Record{
		Type:      t,
		Timestamp: time.Now(),
		Data:      raw,
	}
}

func (r Record) Marshal() []byte {
	b, _ := json.Marshal(r)
	return b
}

func Parse(line []byte) (Record, error) {
	var r Record
	err := json.Unmarshal(line, &r)
	return r, err
}

func ParseData[T any](r Record) (T, error) {
	var v T
	err := json.Unmarshal(r.Data, &v)
	return v, err
}
