package service

import (
	"context"
	"encoding/json"
	"testing"

	toolpb "github.com/chainreactors/aiscan/aop/tool"
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

	err = ingestor.IngestArtifact(context.Background(), &toolpb.Artifact{
		CallId: "scan-1", Tool: "gogo",
		Data: []byte(`{"ip":"192.0.2.1","port":"80","protocol":"tcp","status":"200","uri":"http://192.0.2.1/","title":"Test"}`),
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

func TestCSTXArtifactIngestorNormalizesAIScanWebSummary(t *testing.T) {
	store := &artifactTestStore{}
	ingestor, err := NewArtifactIngestor(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ingestor.Close() })

	err = ingestor.IngestArtifact(context.Background(), &toolpb.Artifact{
		CallId: "curl-1", Tool: "aiscan",
		Data: []byte(`{"url":"https://example.com/","status":200,"content_type":"text/plain","body_length":5}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.operationID != "curl-1" {
		t.Fatalf("operation = %q", store.operationID)
	}
	types := make(map[string]bool)
	for _, raw := range store.nodes {
		var node struct {
			Type        string `json:"cstx_type"`
			StatusCode  int    `json:"status_code"`
			BodyLength  int64  `json:"body_length"`
			ContentType string `json:"content_type"`
		}
		if err := json.Unmarshal(raw, &node); err != nil {
			t.Fatal(err)
		}
		types[node.Type] = true
		if node.StatusCode != 200 || node.BodyLength != 5 || node.ContentType != "text/plain" {
			t.Fatalf("normalized node = %+v", node)
		}
	}
	if !types["url"] || !types["app"] {
		t.Fatalf("normalized types = %v", types)
	}
}
