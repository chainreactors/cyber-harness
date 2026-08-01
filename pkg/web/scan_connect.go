package web

import (
	"context"

	"connectrpc.com/connect"
	scanpb "github.com/chainreactors/aiscan/aop/aiscan/scan"
	"github.com/chainreactors/aiscan/aop/aiscan/scan/scanconnect"
)

type connectScanServer struct {
	scanconnect.UnimplementedScanServiceHandler
	core *scanServiceCore
}

func newConnectScanServer(service *Service) scanconnect.ScanServiceHandler {
	return &connectScanServer{core: newScanServiceCore(service)}
}

func (s *connectScanServer) SubmitScan(ctx context.Context, req *connect.Request[scanpb.SubmitScanRequest]) (*connect.Response[scanpb.SubmitScanResponse], error) {
	response, err := s.core.SubmitScan(ctx, req.Msg)
	if err != nil {
		return nil, asConnectScanError(err)
	}
	return connect.NewResponse(response), nil
}

func (s *connectScanServer) GetScan(ctx context.Context, req *connect.Request[scanpb.GetScanRequest]) (*connect.Response[scanpb.GetScanResponse], error) {
	response, err := s.core.GetScan(ctx, req.Msg)
	if err != nil {
		return nil, asConnectScanError(err)
	}
	return connect.NewResponse(response), nil
}

func (s *connectScanServer) ListScans(ctx context.Context, req *connect.Request[scanpb.ListScansRequest]) (*connect.Response[scanpb.ListScansResponse], error) {
	response, err := s.core.ListScans(ctx, req.Msg)
	if err != nil {
		return nil, asConnectScanError(err)
	}
	return connect.NewResponse(response), nil
}

func (s *connectScanServer) CancelScan(ctx context.Context, req *connect.Request[scanpb.CancelScanRequest]) (*connect.Response[scanpb.CancelScanResponse], error) {
	response, err := s.core.CancelScan(ctx, req.Msg)
	if err != nil {
		return nil, asConnectScanError(err)
	}
	return connect.NewResponse(response), nil
}

func (s *connectScanServer) WatchScanEvents(ctx context.Context, req *connect.Request[scanpb.WatchScanEventsRequest], stream *connect.ServerStream[scanpb.WatchScanEventsResponse]) error {
	return asConnectScanError(s.core.WatchScanEvents(req.Msg, ctx, stream.Send))
}

func (s *connectScanServer) GetScanReport(ctx context.Context, req *connect.Request[scanpb.GetScanReportRequest]) (*connect.Response[scanpb.GetScanReportResponse], error) {
	response, err := s.core.GetScanReport(ctx, req.Msg)
	if err != nil {
		return nil, asConnectScanError(err)
	}
	return connect.NewResponse(response), nil
}

var _ scanconnect.ScanServiceHandler = (*connectScanServer)(nil)
