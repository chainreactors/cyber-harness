package web

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"connectrpc.com/connect"
	aop "github.com/chainreactors/aiscan/aop"
	aopsco "github.com/chainreactors/aiscan/aop/sco"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	types "github.com/chainreactors/aiscan/pkg/types"
	cstx "github.com/chainreactors/libcstx/go"
)

type connectSCOServer struct {
	rpc.UnimplementedSCOServiceHandler
	service *Service
}

func (s *connectSCOServer) ListNodes(ctx context.Context, req *connect.Request[types.ListNodesRequest]) (*connect.Response[types.ListNodesResponse], error) {
	limit := int(req.Msg.GetLimit())
	if limit == 0 {
		limit = 500
	}
	nodes, err := s.service.store.ListSCONodesByScanID(ctx, req.Msg.GetOperationId(), req.Msg.GetType(), limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	encoded := make([][]byte, 0, len(nodes))
	for _, node := range nodes {
		encoded = append(encoded, append([]byte(nil), node...))
	}
	return connect.NewResponse(&types.ListNodesResponse{Nodes: &aopsco.Nodes{Nodes: encoded, MediaType: aop.JSONMediaType}}), nil
}

func (s *connectSCOServer) GetNode(ctx context.Context, req *connect.Request[types.GetNodeRequest]) (*connect.Response[types.GetNodeResponse], error) {
	node, err := s.service.store.GetSCONode(ctx, req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&types.GetNodeResponse{Node: node, MediaType: aop.JSONMediaType}), nil
}

func (s *connectSCOServer) GetStats(ctx context.Context, _ *connect.Request[types.GetStatsRequest]) (*connect.Response[types.GetStatsResponse], error) {
	stats, err := s.service.store.SCONodeStats(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	values := make(map[string]uint64, len(stats))
	for name, count := range stats {
		values[name] = uint64(count)
	}
	return connect.NewResponse(&types.GetStatsResponse{Values: values}), nil
}

func (s *connectSCOServer) DeleteNodes(ctx context.Context, req *connect.Request[types.DeleteNodesRequest]) (*connect.Response[types.DeleteNodesResponse], error) {
	if strings.TrimSpace(req.Msg.GetOperationId()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("operation_id is required"))
	}
	if err := s.service.store.DeleteSCONodesByScan(ctx, req.Msg.GetOperationId()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&types.DeleteNodesResponse{}), nil
}

func (s *connectSCOServer) ImportNodes(ctx context.Context, req *connect.Request[types.ImportNodesRequest]) (*connect.Response[types.ImportNodesResponse], error) {
	if len(req.Msg.GetData()) > 50<<20 {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("import exceeds 50 MiB"))
	}
	artifact := strings.TrimSpace(req.Msg.GetArtifact())
	if artifact == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("artifact is required"))
	}
	nodes, err := cstx.Parse(artifact, req.Msg.GetData())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
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
	operationID := strings.TrimSpace(req.Msg.GetOperationId())
	if operationID == "" {
		operationID = "import"
	}
	if err := s.service.store.UpsertSCONodes(ctx, operationID, raw); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&types.ImportNodesResponse{Nodes: uint64(len(raw)), Duplicates: uint64(len(nodes) - len(raw)), Artifact: artifact}), nil
}

func (s *connectSCOServer) ListArtifacts(context.Context, *connect.Request[types.ListArtifactsRequest]) (*connect.Response[types.ListArtifactsResponse], error) {
	return connect.NewResponse(&types.ListArtifactsResponse{Artifacts: cstx.SupportedArtifacts()}), nil
}

var _ rpc.SCOServiceHandler = (*connectSCOServer)(nil)
