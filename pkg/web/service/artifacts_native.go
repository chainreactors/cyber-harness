package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/core/output"
	cstx "github.com/chainreactors/libcstx/go"
)

type cstxArtifactIngestor struct {
	mu        sync.Mutex
	store     SCOStore
	runtime   *cstx.CSTX
	artifacts []string
}

func NewArtifactIngestor(store SCOStore) (ArtifactIngestor, error) {
	if store == nil {
		return nil, fmt.Errorf("artifact ingestor: SCO store is required")
	}
	runtime, err := cstx.Open(context.Background(), cstx.Config{ProjectID: "aiscan"})
	if err != nil {
		return nil, fmt.Errorf("open CSTX runtime: %w", err)
	}
	if err := runtime.Schemas.LoadPlugin(context.Background(), "easm"); err != nil {
		_ = runtime.Close()
		return nil, fmt.Errorf("load CSTX EASM plugin: %w", err)
	}
	artifacts, err := runtime.Schemas.PluginArtifacts(context.Background(), "easm")
	if err != nil {
		_ = runtime.Close()
		return nil, fmt.Errorf("list CSTX EASM artifacts: %w", err)
	}
	return &cstxArtifactIngestor{store: store, runtime: runtime, artifacts: artifacts}, nil
}

func (i *cstxArtifactIngestor) IngestArtifact(ctx context.Context, operationID string, artifact output.ToolArtifact) error {
	_, _, err := i.NormalizeArtifact(ctx, operationID, artifact.Tool, artifact.Data)
	return err
}

func (i *cstxArtifactIngestor) NormalizeArtifact(ctx context.Context, operationID, artifact string, data []byte) (uint64, uint64, error) {
	artifact = strings.TrimSpace(artifact)
	if artifact == "" {
		return 0, 0, fmt.Errorf("artifact is required")
	}
	if len(data) == 0 {
		return 0, 0, nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	payload := append([]byte(nil), data...)
	if payload[len(payload)-1] != '\n' {
		payload = append(payload, '\n')
	}
	resultJSON, err := i.runtime.Raw.IngestNative(ctx, "easm", artifact, payload)
	if err != nil {
		return 0, 0, err
	}
	var result struct {
		NodeIDs []string `json:"node_ids"`
	}
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		return 0, 0, fmt.Errorf("decode CSTX ingest result: %w", err)
	}
	seen := make(map[string]struct{}, len(result.NodeIDs))
	raw := make([]json.RawMessage, 0, len(result.NodeIDs))
	for _, nodeID := range result.NodeIDs {
		if nodeID == "" {
			continue
		}
		if _, duplicate := seen[nodeID]; duplicate {
			continue
		}
		seen[nodeID] = struct{}{}
		node, err := i.runtime.Graph.Node(ctx, nodeID)
		if err != nil {
			return 0, 0, fmt.Errorf("load CSTX node %s: %w", nodeID, err)
		}
		encoded, err := encodeSCONode(node)
		if err != nil {
			return 0, 0, err
		}
		raw = append(raw, encoded)
	}
	if operationID == "" {
		operationID = "import"
	}
	if err := i.store.UpsertSCONodes(ctx, operationID, raw); err != nil {
		return 0, 0, fmt.Errorf("persist SCO nodes: %w", err)
	}
	return uint64(len(raw)), uint64(len(result.NodeIDs) - len(raw)), nil
}

func (i *cstxArtifactIngestor) SupportedArtifacts() []string {
	return append([]string(nil), i.artifacts...)
}

func (i *cstxArtifactIngestor) Close() error {
	if i == nil || i.runtime == nil {
		return nil
	}
	return i.runtime.Close()
}

func encodeSCONode(node cstx.Node) (json.RawMessage, error) {
	document := make(map[string]any, len(node.Model)+2)
	for key, value := range node.Model {
		document[key] = value
	}
	document["cstx_id"] = node.ID
	document["cstx_type"] = node.Type
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode CSTX node %s: %w", node.ID, err)
	}
	return encoded, nil
}
