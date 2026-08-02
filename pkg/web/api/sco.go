package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
	aopsco "github.com/chainreactors/aiscan/aop/sco"
	types "github.com/chainreactors/aiscan/pkg/types"
	cstx "github.com/chainreactors/libcstx/go"
)

type SCOStore interface {
	ListSCONodesByScanID(context.Context, string, string, int) ([]json.RawMessage, error)
	GetSCONode(context.Context, string) (json.RawMessage, error)
	SCONodeStats(context.Context) (map[string]int, error)
	DeleteSCONodesByScan(context.Context, string) error
	UpsertSCONodes(context.Context, string, []json.RawMessage) error
}

type SCO struct {
	store SCOStore
}

func NewSCO(store SCOStore) *SCO { return &SCO{store: store} }

func (s *SCO) ListNodes(ctx context.Context, request *types.ListNodesRequest) (*types.ListNodesResponse, error) {
	if s == nil || s.store == nil {
		return nil, Errorf(CodeFailedPrecondition, "SCO store is unavailable")
	}
	if request == nil {
		request = new(types.ListNodesRequest)
	}
	limit := int(request.GetLimit())
	if limit == 0 {
		limit = 500
	}
	nodes, err := s.store.ListSCONodesByScanID(ctx, request.GetOperationId(), request.GetType(), limit)
	if err != nil {
		return nil, err
	}
	encoded := make([][]byte, 0, len(nodes))
	for _, node := range nodes {
		encoded = append(encoded, append([]byte(nil), node...))
	}
	return &types.ListNodesResponse{Nodes: &aopsco.Nodes{Nodes: encoded, MediaType: aop.JSONMediaType}}, nil
}

func (s *SCO) GetNode(ctx context.Context, request *types.GetNodeRequest) (*types.GetNodeResponse, error) {
	if s == nil || s.store == nil {
		return nil, Errorf(CodeFailedPrecondition, "SCO store is unavailable")
	}
	if request == nil || strings.TrimSpace(request.GetId()) == "" {
		return nil, Errorf(CodeInvalidArgument, "id is required")
	}
	node, err := s.store.GetSCONode(ctx, request.GetId())
	if err != nil {
		return nil, NewError(CodeNotFound, err)
	}
	return &types.GetNodeResponse{Node: node, MediaType: aop.JSONMediaType}, nil
}

func (s *SCO) GetStats(ctx context.Context, _ *types.GetStatsRequest) (*types.GetStatsResponse, error) {
	if s == nil || s.store == nil {
		return nil, Errorf(CodeFailedPrecondition, "SCO store is unavailable")
	}
	stats, err := s.store.SCONodeStats(ctx)
	if err != nil {
		return nil, err
	}
	values := make(map[string]uint64, len(stats))
	for name, count := range stats {
		values[name] = uint64(count)
	}
	return &types.GetStatsResponse{Values: values}, nil
}

func (s *SCO) DeleteNodes(ctx context.Context, request *types.DeleteNodesRequest) (*types.DeleteNodesResponse, error) {
	if s == nil || s.store == nil {
		return nil, Errorf(CodeFailedPrecondition, "SCO store is unavailable")
	}
	if request == nil || strings.TrimSpace(request.GetOperationId()) == "" {
		return nil, Errorf(CodeInvalidArgument, "operation_id is required")
	}
	if err := s.store.DeleteSCONodesByScan(ctx, request.GetOperationId()); err != nil {
		return nil, err
	}
	return &types.DeleteNodesResponse{}, nil
}

func (s *SCO) ImportNodes(ctx context.Context, request *types.ImportNodesRequest) (*types.ImportNodesResponse, error) {
	if s == nil || s.store == nil {
		return nil, Errorf(CodeFailedPrecondition, "SCO store is unavailable")
	}
	if request == nil {
		return nil, Errorf(CodeInvalidArgument, "request is required")
	}
	if len(request.GetData()) > 50<<20 {
		return nil, Errorf(CodeResourceExhausted, "import exceeds 50 MiB")
	}
	artifact := strings.TrimSpace(request.GetArtifact())
	if artifact == "" {
		return nil, Errorf(CodeInvalidArgument, "artifact is required")
	}
	nodes, err := cstx.Parse(artifact, request.GetData())
	if err != nil {
		return nil, NewError(CodeInvalidArgument, err)
	}
	seen := make(map[string]struct{}, len(nodes))
	raw := make([]json.RawMessage, 0, len(nodes))
	for _, node := range nodes {
		if _, ok := seen[node.CstxID()]; ok {
			continue
		}
		seen[node.CstxID()] = struct{}{}
		encoded, err := json.Marshal(node)
		if err == nil {
			raw = append(raw, encoded)
		}
	}
	operationID := strings.TrimSpace(request.GetOperationId())
	if operationID == "" {
		operationID = "import"
	}
	if err := s.store.UpsertSCONodes(ctx, operationID, raw); err != nil {
		return nil, fmt.Errorf("import SCO nodes: %w", err)
	}
	return &types.ImportNodesResponse{Nodes: uint64(len(raw)), Duplicates: uint64(len(nodes) - len(raw)), Artifact: artifact}, nil
}

func (*SCO) ListArtifacts(context.Context, *types.ListArtifactsRequest) (*types.ListArtifactsResponse, error) {
	return &types.ListArtifactsResponse{Artifacts: cstx.SupportedArtifacts()}, nil
}
