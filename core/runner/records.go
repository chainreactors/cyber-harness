package runner

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/chainreactors/aiscan/core/output"
)

func WSPayloadToRecord(msgType, taskID, agentID string, payload json.RawMessage) *output.Record {
	rec := &output.Record{
		Timestamp: time.Now(),
		Data:      payload,
		ID:        genRecordID(),
		ScanID:    taskID,
		AgentID:   agentID,
	}

	var meta struct {
		Turn       int    `json:"turn"`
		ToolName   string `json:"tool_name"`
		ToolCallID string `json:"tool_call_id"`
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &meta)
	}
	rec.Turn = meta.Turn
	rec.Source = meta.ToolName

	switch msgType {
	case "agent.tool_execution_start":
		rec.Type = output.TypeToolCall
		rec.Summary = meta.ToolName
	case "agent.tool_execution_end":
		rec.Type = output.TypeToolResult
		rec.Summary = meta.ToolName
	case "agent.message_end":
		rec.Type = output.TypeMessage
		rec.Source = "agent"
	case "agent.turn_end":
		rec.Type = output.TypeTurnEnd
		rec.Source = "agent"
	case "agent.llm_request":
		rec.Type = output.TypeLLMRequest
		rec.Source = "agent"
	default:
		rec.Type = output.TypeAgent
		rec.Source = "agent"
	}
	return rec
}

func ResultToRecords(scanID, agentID string, result *output.Result) []*output.Record {
	if result == nil {
		return nil
	}
	var recs []*output.Record
	now := time.Now()

	for _, loot := range result.Loots {
		rec := &output.Record{
			Type:      lootTypeToRecordType(loot.Kind),
			Timestamp: now,
			Loot:      true,
			ID:        genRecordID(),
			ScanID:    scanID,
			AgentID:   agentID,
			Source:    loot.Kind,
			Target:    loot.Target,
			Priority:  loot.Priority,
			Summary:   loot.Description,
			Tags:      loot.Tags,
		}
		rec.Data = marshalAny(loot)
		recs = append(recs, rec)
	}

	for _, e := range result.Errors {
		recs = append(recs, &output.Record{
			Type:      output.TypeError,
			Timestamp: now,
			Data:      marshalAny(e),
			ID:        genRecordID(),
			ScanID:    scanID,
			AgentID:   agentID,
			Source:    e.Source,
			Summary:   e.Message,
		})
	}

	return recs
}

func lootTypeToRecordType(kind string) output.RecordType {
	switch kind {
	case output.LootVuln:
		return output.TypeNeutron
	case output.LootWeakpass:
		return output.TypeZombie
	case output.LootFingerprint:
		return output.TypeGogo
	default:
		return output.RecordType(kind)
	}
}

var recordIDSeq atomic.Uint64

func genRecordID() string {
	seq := recordIDSeq.Add(1)
	return fmt.Sprintf("r%d-%d", time.Now().UnixNano(), seq)
}

func marshalAny(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
