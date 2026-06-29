package output

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type RecordType string

const (
	TypeScanStart RecordType = "scan_start"
	TypeGogo      RecordType = "gogo"
	TypeSpray     RecordType = "spray"
	TypeZombie    RecordType = "zombie"
	TypeNeutron   RecordType = "neutron"
	TypeAgent     RecordType = "agent"
	TypeScanEnd   RecordType = "scan_end"

	TypeToolCall   RecordType = "tool_call"
	TypeToolResult RecordType = "tool_result"
	TypeMessage    RecordType = "message"
	TypeMetric     RecordType = "metric"
	TypeLLMRequest RecordType = "llm_request"
	TypeTurnEnd    RecordType = "turn_end"
	TypeAgentEnd   RecordType = "agent_end"
	TypeError      RecordType = "error"
)

type Record struct {
	Type      RecordType      `json:"type"`
	Timestamp time.Time       `json:"ts"`
	Loot      bool            `json:"loot,omitempty"`
	Data      json.RawMessage `json:"data"`
	ID        string          `json:"id,omitempty"`
	ScanID    string          `json:"scan_id,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	AgentID   string          `json:"agent_id,omitempty"`
	Source    string          `json:"source,omitempty"`
	Target    string          `json:"target,omitempty"`
	Turn      int             `json:"turn,omitempty"`
	Priority  string          `json:"priority,omitempty"`
	Summary   string          `json:"summary,omitempty"`
	Tags      []string        `json:"tags,omitempty"`
}


type RecordFilter struct {
	ScanID    string
	SessionID string
	AgentID   string
	Type      RecordType
	Types     []RecordType
	Source    string
	Target    string
	Priority  string
	LootOnly  bool
	Tags      []string
	Limit     int
	Offset    int
}

type RecordSummary struct {
	Total      int            `json:"total"`
	ByType     map[string]int `json:"by_type"`
	ByPriority map[string]int `json:"by_priority"`
	BySource   map[string]int `json:"by_source"`
}

func NewRecord(t RecordType, data interface{}) Record {
	raw, _ := json.Marshal(data)
	return Record{
		Type:      t,
		Timestamp: time.Now(),
		Data:      raw,
	}
}

func NewLootRecord(t RecordType, data interface{}) Record {
	r := NewRecord(t, data)
	r.Loot = true
	return r
}

func (r Record) Marshal() []byte {
	b, _ := json.Marshal(r)
	return b
}

func ParseRecord(line []byte) (Record, error) {
	var r Record
	err := json.Unmarshal(line, &r)
	return r, err
}

func ParseRecordData[T any](r Record) (T, error) {
	var v T
	err := json.Unmarshal(r.Data, &v)
	return v, err
}

func ParseRecordFile(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var records []Record
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		r, err := ParseRecord(line)
		if err != nil {
			continue
		}
		records = append(records, r)
	}
	return records, scanner.Err()
}

func RenderFile(path, format, outputPath string) error {
	var w io.Writer = os.Stdout
	if outputPath != "" {
		outFile, err := os.Create(outputPath)
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}
		defer outFile.Close()
		w = outFile
	}

	entries, err := ParseTimelineFile(path)
	if err != nil {
		return err
	}

	switch strings.ToLower(format) {
	case "markdown", "md":
		return RenderTimelineMarkdown(w, entries)
	default:
		return RenderTimeline(w, entries)
	}
}
