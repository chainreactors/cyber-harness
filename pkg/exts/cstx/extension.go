//go:build cstx && cgo

package cstx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/extension"
	libcstx "github.com/chainreactors/libcstx/go"
	"github.com/chainreactors/libcstx/go/proto/cstxproto"
)

const nativeExtension = "easm"

// aiscanArtifact is the producer name the orchestrator uses for its own
// observations, as opposed to the name of a scanner-native format.
const aiscanArtifact = "aiscan"

// ArtifactStore is the persistence capability required by the importer. It is
// intentionally smaller than the management API's SCOStore: importing
// observations can append nodes but cannot query or delete them.
type ArtifactStore interface {
	UpsertSCONodes(context.Context, string, []json.RawMessage) error
}

// Importer is the import surface the web layer consumes. It mirrors
// managementapi.ArtifactImporter, which the caller assigns it to, without
// pulling the web API's dependency closure into this package.
type Importer interface {
	ImportArtifact(context.Context, string, *toolpb.Artifact) (uint64, uint64, error)
	ArtifactTypes() []string
}

// Extension owns the native CSTX runtime and the SCO import path. The runtime
// carries cgo, so this whole package is gated on the cstx tag.
type Extension struct {
	mu        sync.Mutex
	store     ArtifactStore
	runtime   *libcstx.CSTX
	artifacts []string
	closed    bool
}

var _ extension.Extension = (*Extension)(nil)
var _ Importer = (*Extension)(nil)

// New declares the extension without touching the runtime; Load opens it.
func New(store ArtifactStore) (*Extension, error) {
	if store == nil {
		return nil, fmt.Errorf("cstx extension: SCO store is required")
	}
	return &Extension{store: store}, nil
}

// Load opens the CSTX runtime and enables the extension that reads
// scanner-native records.
func (e *Extension) Load(scope *extension.Scope) error {
	ctx := scope.Init()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return fmt.Errorf("cstx extension is closed")
	}
	if e.runtime != nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime, err := libcstx.Open(ctx, &cstxproto.RuntimeConfig{ProjectId: "aiscan"})
	if err != nil {
		return fmt.Errorf("open CSTX runtime: %w", err)
	}
	if err := runtime.Extensions.Enable(ctx, nativeExtension); err != nil {
		_ = runtime.Close()
		return fmt.Errorf("enable CSTX %s extension: %w", nativeExtension, err)
	}
	info, err := runtime.Extensions.Info(ctx, nativeExtension)
	if err != nil {
		_ = runtime.Close()
		return fmt.Errorf("read CSTX %s extension: %w", nativeExtension, err)
	}
	e.runtime = runtime
	e.artifacts = info.GetArtifacts()
	return nil
}

// Importer returns the import surface, or nil before Load and after Close.
func (e *Extension) Importer() Importer {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.runtime == nil {
		return nil
	}
	return e
}

// ImportArtifact runs one artifact through the native parser and persists the
// resulting SCO nodes. The return is the number of distinct nodes written and
// the number of reports collapsed into them.
func (e *Extension) ImportArtifact(ctx context.Context, operationID string, value *toolpb.Artifact) (uint64, uint64, error) {
	if value == nil {
		return 0, 0, fmt.Errorf("artifact is required")
	}
	producer := strings.TrimSpace(value.GetTool())
	if producer == "" {
		return 0, 0, fmt.Errorf("artifact is required")
	}
	artifact := nativeArtifact(producer)
	data := value.GetData()
	if len(data) == 0 {
		return 0, 0, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runtime == nil {
		return 0, 0, fmt.Errorf("cstx extension is not loaded")
	}
	payload := append([]byte(nil), data...)
	if payload[len(payload)-1] != '\n' {
		payload = append(payload, '\n')
	}
	// Parsing is only the accelerator: the batch it returns is written through
	// the same merge/link path a parser implemented in any other language uses,
	// so node identity and relationships come from the runtime, not the parser.
	batch, _, err := e.runtime.Graph.Parse(ctx, &cstxproto.ParserPayload{
		Plugin:      nativeExtension,
		Artifact:    artifact,
		Data:        payload,
		ContentType: value.GetMediaType(),
	})
	if err != nil {
		return 0, 0, err
	}
	if _, err := e.runtime.Graph.AddNodes(ctx, batch.GetNodes()); err != nil {
		return 0, 0, err
	}
	change, err := e.runtime.LastChange(ctx)
	if err != nil {
		return 0, 0, err
	}
	reported := len(change.GetAddedNodeIds()) + len(change.GetUpdatedNodeIds())
	nodeIDs := dedupeNodeIDs(change.GetAddedNodeIds(), change.GetUpdatedNodeIds())
	// The parser that produced the nodes may differ from the producer, so the
	// relationships record who observed the data, not which schema read it.
	if _, err := e.runtime.Graph.Link(ctx, nodeIDs, producer); err != nil {
		return 0, 0, err
	}
	raw := make([]json.RawMessage, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		node, err := e.runtime.Graph.Node(ctx, nodeID)
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
	if err := e.store.UpsertSCONodes(ctx, operationID, raw); err != nil {
		return 0, 0, fmt.Errorf("persist SCO nodes: %w", err)
	}
	return uint64(len(raw)), uint64(reported - len(raw)), nil
}

// ArtifactTypes lists the artifact names the native parser reads.
func (e *Extension) ArtifactTypes() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.artifacts...)
}

// Close releases the CSTX runtime. The owning Set serializes this call.
func (e *Extension) Close(context.Context) error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	runtime := e.runtime
	e.mu.Unlock()
	if runtime != nil {
		if err := runtime.Close(); err != nil {
			return err
		}
	}
	e.mu.Lock()
	e.runtime = nil
	e.artifacts = nil
	e.closed = true
	e.mu.Unlock()
	return nil
}

// nativeArtifact resolves the CSTX artifact whose schema a producer's records
// follow. A tool that is itself a scanner-native format names its own schema;
// the orchestrator is not a format, so its compact HTTP observations are read
// with the web schema CSTX calls "spray".
func nativeArtifact(producer string) string {
	if producer == aiscanArtifact {
		return "spray"
	}
	return producer
}

// dedupeNodeIDs keeps the first occurrence of each non-empty id, in order.
func dedupeNodeIDs(groups ...[]string) []string {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	seen := make(map[string]struct{}, total)
	ids := make([]string, 0, total)
	for _, group := range groups {
		for _, nodeID := range group {
			if nodeID == "" {
				continue
			}
			if _, duplicate := seen[nodeID]; duplicate {
				continue
			}
			seen[nodeID] = struct{}{}
			ids = append(ids, nodeID)
		}
	}
	return ids
}

func encodeSCONode(node *cstxproto.Node) (json.RawMessage, error) {
	nodeType, values, err := libcstx.FieldValues(node)
	if err != nil {
		return nil, err
	}
	document := make(map[string]any, len(values)+2)
	for key, value := range values {
		document[key] = value
	}
	document["cstx_id"] = node.GetId()
	document["cstx_type"] = nodeType
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode CSTX node %s: %w", node.GetId(), err)
	}
	return encoded, nil
}
