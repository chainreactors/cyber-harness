package output

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
)

// renderEventsCSV exports recorded scanner artifacts, joined with their final
// loot markers. Native JSON fields stay in data.* columns; no scanner or model
// is run to reconstruct the result.
func renderEventsCSV(w io.Writer, events []*aop.Event) error {
	headers := []string{"target", "tool", "kind", "priority", "verification_status", "description", "tags", "result_id", "session_id", "turn_id", "timestamp"}
	var rows []map[string]string
	columns := make(map[string]bool)
	artifacts := make(map[[4]string]int)
	loots := make(map[[4]string]*toolpb.Loot)
	seen := make(map[[3]string]bool)
	for _, event := range events {
		if event == nil {
			continue
		}
		if event.Id != "" {
			key := [3]string{event.SessionId, event.Emitter, event.Id}
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		extension := event.GetExtension()
		loot := new(toolpb.Loot)
		if extension != nil && extension.MessageIs(loot) {
			if err := extension.UnmarshalTo(loot); err != nil {
				return fmt.Errorf("event %s: decode loot: %w", event.Id, err)
			}
			if loot.ResultId != "" {
				loots[[4]string{event.SessionId, event.TurnId, loot.Tool, loot.ResultId}] = loot
			}
			continue
		}
		artifact, _, found, err := toolpb.FromEvent(event)
		if err != nil {
			return fmt.Errorf("event %s: %w", event.Id, err)
		}
		if !found {
			continue
		}
		row := map[string]string{
			"target": artifact.Target, "tool": artifact.Tool, "kind": artifact.Kind,
			"result_id": artifact.ResultId, "session_id": event.SessionId, "turn_id": event.TurnId,
		}
		stamp := artifact.Timestamp
		if stamp == nil {
			stamp = event.EmittedAt
		}
		if stamp != nil {
			row["timestamp"] = stamp.AsTime().UTC().Format(time.RFC3339Nano)
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(artifact.Data, &data); err != nil || data == nil {
			return fmt.Errorf("event %s: scanner artifact data must be a JSON object", event.Id)
		}
		for field, raw := range data {
			name := "data." + field
			columns[name] = true
			var value string
			if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
				var compact bytes.Buffer
				if err := json.Compact(&compact, raw); err != nil {
					return err
				}
				value = compact.String()
			}
			row[name] = value
		}
		key := [4]string{event.SessionId, event.TurnId, artifact.Tool, artifact.ResultId}
		if index, exists := artifacts[key]; exists && artifact.ResultId != "" {
			rows[index] = row
		} else {
			artifacts[key] = len(rows)
			rows = append(rows, row)
		}
	}
	for key, loot := range loots {
		if index, ok := artifacts[key]; ok {
			row := rows[index]
			row["priority"], row["verification_status"] = loot.Priority, loot.VerificationStatus
			row["description"], row["tags"] = loot.Description, strings.Join(loot.Tags, "; ")
		}
	}
	var fields []string
	for field := range columns {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	headers = append(headers, fields...)
	writer := csv.NewWriter(w)
	writer.UseCRLF = true
	if err := writer.Write(headers); err != nil {
		return err
	}
	for _, row := range rows {
		record := make([]string, len(headers))
		for i, field := range headers {
			record[i] = row[field]
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}
