package web

import (
	"context"

	scanpb "github.com/chainreactors/aiscan/aop/aiscan/scan"
)

type grpcScanServer struct {
	scanpb.UnimplementedScanServiceServer
	core *scanServiceCore
}

func newGRPCScanServer(service *Service) scanpb.ScanServiceServer {
	return &grpcScanServer{core: newScanServiceCore(service)}
}

func (s *grpcScanServer) SubmitScan(ctx context.Context, req *scanpb.SubmitScanRequest) (*scanpb.SubmitScanResponse, error) {
	return s.core.SubmitScan(ctx, req)
}

func (s *grpcScanServer) GetScan(ctx context.Context, req *scanpb.GetScanRequest) (*scanpb.GetScanResponse, error) {
	return s.core.GetScan(ctx, req)
}

func (s *grpcScanServer) ListScans(ctx context.Context, req *scanpb.ListScansRequest) (*scanpb.ListScansResponse, error) {
	return s.core.ListScans(ctx, req)
}

func (s *grpcScanServer) CancelScan(ctx context.Context, req *scanpb.CancelScanRequest) (*scanpb.CancelScanResponse, error) {
	return s.core.CancelScan(ctx, req)
}

func (s *grpcScanServer) WatchScanEvents(req *scanpb.WatchScanEventsRequest, stream scanpb.ScanService_WatchScanEventsServer) error {
	return s.core.WatchScanEvents(req, stream.Context(), func(response *scanpb.WatchScanEventsResponse) error {
		return stream.Send(response)
	})
}

func (s *grpcScanServer) GetScanReport(ctx context.Context, req *scanpb.GetScanReportRequest) (*scanpb.GetScanReportResponse, error) {
	return s.core.GetScanReport(ctx, req)
}

var _ scanpb.ScanServiceServer = (*grpcScanServer)(nil)
