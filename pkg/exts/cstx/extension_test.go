//go:build full && cgo

package cstx

import (
	"context"
	"encoding/json"
	"testing"

	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
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

func loadImporter(t *testing.T, store ArtifactStore) coretool.ArtifactImporter {
	t.Helper()
	instance, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	importer := instance.Importer()
	if importer == nil {
		t.Fatal("importer is unavailable after Load")
	}
	return importer
}

func TestNewRequiresStore(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New without a store must fail")
	}
}

func TestImporterIsUnavailableOutsideLoad(t *testing.T) {
	store := &artifactTestStore{}
	instance, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Importer() != nil {
		t.Fatal("importer must be nil before Load")
	}
	set, err := extension.New(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if instance.Importer() == nil {
		t.Fatal("importer must be available after Load")
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if instance.Importer() != nil {
		t.Fatal("importer must be nil after Close")
	}
}

func TestImporterNormalizesOnServer(t *testing.T) {
	store := &artifactTestStore{}
	importer := loadImporter(t, store)

	_, _, err := importer.ImportArtifact(context.Background(), "scan-1", &toolpb.Artifact{Tool: "gogo",
		Data: []byte(`{"ip":"192.0.2.1","port":"80","protocol":"tcp","status":"200","uri":"http://192.0.2.1/","title":"Test"}`)})
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

func TestImporterNormalizesCyberWebSummary(t *testing.T) {
	store := &artifactTestStore{}
	importer := loadImporter(t, store)

	_, _, err := importer.ImportArtifact(context.Background(), "curl-1", &toolpb.Artifact{Tool: "cyber",
		Data: []byte(`{"url":"https://example.com/","status":200,"content_type":"text/plain","body_length":5}`)})
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
