package api

import (
	"context"
	"encoding/json"
	"strings"

	aop "github.com/chainreactors/cyber/aop"
	aopsco "github.com/chainreactors/cyber/aop/sco"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	coretool "github.com/chainreactors/cyber/core/tool"
	types "github.com/chainreactors/cyber/core/types"
)

type SCOStore interface {
	ListSCONodesByScanID(context.Context, string, string, int) ([]json.RawMessage, error)
	GetSCONode(context.Context, string) (json.RawMessage, error)
	SCONodeStats(context.Context) (map[string]int, error)
	DeleteSCONodesByScan(context.Context, string) error
	UpsertSCONodes(context.Context, string, []json.RawMessage) error
}

type SCO struct {
	store     SCOStore
	artifacts coretool.ArtifactImporter
}

func NewSCO(store SCOStore, artifacts coretool.ArtifactImporter) *SCO {
	return &SCO{store: store, artifacts: artifacts}
}

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
	if s.artifacts == nil {
		return nil, Errorf(CodeFailedPrecondition, "artifact import is unavailable")
	}
	operationID := strings.TrimSpace(request.GetOperationId())
	if operationID == "" {
		operationID = "import"
	}
	nodes, duplicates, err := s.artifacts.ImportArtifact(ctx, operationID, &toolpb.Artifact{Tool: artifact, Data: request.GetData()})
	if err != nil {
		return nil, NewError(CodeInvalidArgument, err)
	}
	return &types.ImportNodesResponse{Nodes: nodes, Duplicates: duplicates, Artifact: artifact}, nil
}

func (s *SCO) ListArtifacts(context.Context, *types.ListArtifactsRequest) (*types.ListArtifactsResponse, error) {
	if s == nil || s.artifacts == nil {
		return &types.ListArtifactsResponse{}, nil
	}
	return &types.ListArtifactsResponse{Artifacts: s.artifacts.ArtifactTypes()}, nil
}
