package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/chainreactors/aiscan/core/output"
)

type artifactTestStore struct {
	operationID string
	nodes       []json.RawMessage
}

func (s *artifactTestStore) UpsertSCONodes(_ context.Context, operationID string, nodes []json.RawMessage) error {
	s.operationID = operationID
	s.nodes = append([]json.RawMessage(nil), nodes...)
	return nil
}

func TestCSTXArtifactIngestorNormalizesOnServer(t *testing.T) {
	store := &artifactTestStore{}
	ingestor, err := NewArtifactIngestor(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ingestor.Close() })

	err = ingestor.IngestArtifact(context.Background(), "scan-1", output.ToolArtifact{
		Tool: "gogo",
		Data: json.RawMessage(`{"ip":"192.0.2.1","port":"80","protocol":"tcp","status":"200","uri":"http://192.0.2.1/","title":"Test"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.operationID != "scan-1" || len(store.nodes) == 0 {
		t.Fatalf("operation=%q nodes=%d", store.operationID, len(store.nodes))
	}
	types := make(map[string]bool)
	for _, raw := range store.nodes {
		var header struct {
			Type string `json:"cstx_type"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			t.Fatal(err)
		}
		types[header.Type] = true
	}
	if !types["ip"] || !types["port"] {
		t.Fatalf("normalized types = %v", types)
	}
}
