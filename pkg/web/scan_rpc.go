package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	aop "github.com/chainreactors/aiscan/aop"
	scanpb "github.com/chainreactors/aiscan/aop/aiscan/scan"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type scanServiceCore struct {
	service *Service
}

func newScanServiceCore(service *Service) *scanServiceCore {
	return &scanServiceCore{service: service}
}

func (s *scanServiceCore) SubmitScan(ctx context.Context, request *scanpb.SubmitScanRequest) (*scanpb.SubmitScanResponse, error) {
	if s.service == nil || request == nil || strings.TrimSpace(request.RequestId) == "" {
		return rejectedSubmitScan(request, codes.InvalidArgument, "request_id is required"), nil
	}
	options := request.GetOptions()
	job, err := s.service.SubmitScan(ctx, request.Target, request.Mode, options.GetVerify(), options.GetSniper(), options.GetDeep())
	if err != nil {
		return rejectedSubmitScan(request, codes.InvalidArgument, err.Error()), nil
	}
	return &scanpb.SubmitScanResponse{RequestId: request.RequestId, Outcome: &scanpb.SubmitScanResponse_Accepted{Accepted: scanToProto(job)}}, nil
}

func (s *scanServiceCore) GetScan(ctx context.Context, request *scanpb.GetScanRequest) (*scanpb.GetScanResponse, error) {
	if s.service == nil || request == nil || strings.TrimSpace(request.ScanId) == "" {
		return nil, status.Error(codes.InvalidArgument, "scan_id is required")
	}
	job, err := s.service.GetScan(ctx, request.ScanId)
	if err != nil {
		return nil, scanRPCError(err)
	}
	return &scanpb.GetScanResponse{Scan: scanToProto(job)}, nil
}

func (s *scanServiceCore) ListScans(ctx context.Context, _ *scanpb.ListScansRequest) (*scanpb.ListScansResponse, error) {
	if s.service == nil {
		return nil, status.Error(codes.Unavailable, "scan service is unavailable")
	}
	jobs, err := s.service.ListScans(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	response := &scanpb.ListScansResponse{Scans: make([]*scanpb.Scan, 0, len(jobs))}
	for _, job := range jobs {
		response.Scans = append(response.Scans, scanToProto(job))
	}
	return response, nil
}

func (s *scanServiceCore) CancelScan(ctx context.Context, request *scanpb.CancelScanRequest) (*scanpb.CancelScanResponse, error) {
	if s.service == nil || request == nil || strings.TrimSpace(request.RequestId) == "" || strings.TrimSpace(request.ScanId) == "" {
		return rejectedCancelScan(request, codes.InvalidArgument, "request_id and scan_id are required"), nil
	}
	if err := s.service.CancelScan(request.ScanId); err != nil {
		code := codes.FailedPrecondition
		if errors.Is(err, ErrScanNotFound) {
			code = codes.NotFound
		}
		return rejectedCancelScan(request, code, err.Error()), nil
	}
	job, err := s.service.GetScan(ctx, request.ScanId)
	if err != nil {
		return nil, scanRPCError(err)
	}
	return &scanpb.CancelScanResponse{RequestId: request.RequestId, Outcome: &scanpb.CancelScanResponse_Accepted{Accepted: scanToProto(job)}}, nil
}

func (s *scanServiceCore) WatchScanEvents(request *scanpb.WatchScanEventsRequest, ctx context.Context, send func(*scanpb.WatchScanEventsResponse) error) error {
	if s.service == nil || request == nil || strings.TrimSpace(request.ScanId) == "" {
		return status.Error(codes.InvalidArgument, "scan_id is required")
	}
	if send == nil {
		return status.Error(codes.Internal, "scan event sender is unavailable")
	}
	live, snapshotSequence, unsubscribe := s.service.hub.SubscribeScan(request.ScanId)
	defer unsubscribe()
	job, err := s.service.GetScan(ctx, request.ScanId)
	if err != nil {
		return scanRPCError(err)
	}
	snapshot := scanSnapshot(job, snapshotSequence)
	if err := send(&scanpb.WatchScanEventsResponse{Event: snapshot}); err != nil {
		return err
	}
	if scanTerminal(job.Status) {
		return nil
	}
	last := snapshot.Sequence
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-live:
			if !ok {
				return nil
			}
			if event == nil || event.Sequence <= last {
				continue
			}
			if err := send(&scanpb.WatchScanEventsResponse{Event: event}); err != nil {
				return err
			}
			last = event.Sequence
			if event.GetCompleted() != nil || event.GetFailed() != nil {
				return nil
			}
		}
	}
}

func (s *scanServiceCore) GetScanReport(ctx context.Context, request *scanpb.GetScanReportRequest) (*scanpb.GetScanReportResponse, error) {
	if s.service == nil || request == nil || strings.TrimSpace(request.ScanId) == "" {
		return nil, status.Error(codes.InvalidArgument, "scan_id is required")
	}
	markdown, err := s.service.GetReport(ctx, request.ScanId, request.Language)
	if err != nil {
		return nil, scanRPCError(err)
	}
	if markdown == "" {
		return nil, status.Error(codes.FailedPrecondition, "scan report is not ready")
	}
	return &scanpb.GetScanReportResponse{Markdown: markdown, MediaType: "text/markdown; charset=utf-8"}, nil
}

func scanToProto(job *ScanJob) *scanpb.Scan {
	if job == nil {
		return nil
	}
	var result *aop.EncodedValue
	if job.Result != nil {
		result, _ = aop.JSONValue(job.Result)
	}
	return &scanpb.Scan{
		Id: job.ID, Target: job.Target, Mode: job.Mode,
		Options: &scanpb.ScanOptions{Verify: job.Verify, Sniper: job.Sniper, Deep: job.Deep},
		Status:  scanStatusToProto(job.Status), Progress: job.Progress, Report: job.Report,
		Result: result, Error: job.Error,
		CreatedAt: timestamppb.New(job.CreatedAt), UpdatedAt: timestamppb.New(job.UpdatedAt),
	}
}

func scanStatusToProto(value ScanStatus) scanpb.ScanStatus {
	switch value {
	case StatusQueued:
		return scanpb.ScanStatus_SCAN_STATUS_QUEUED
	case StatusRunning:
		return scanpb.ScanStatus_SCAN_STATUS_RUNNING
	case StatusCompleted:
		return scanpb.ScanStatus_SCAN_STATUS_COMPLETED
	case StatusFailed:
		return scanpb.ScanStatus_SCAN_STATUS_FAILED
	case StatusCanceled:
		return scanpb.ScanStatus_SCAN_STATUS_CANCELED
	default:
		return scanpb.ScanStatus_SCAN_STATUS_UNSPECIFIED
	}
}

func scanSnapshot(job *ScanJob, sequence uint64) *scanpb.ScanEvent {
	return &scanpb.ScanEvent{ScanId: job.ID, Sequence: sequence, EmittedAt: timestamppb.Now(), Payload: &scanpb.ScanEvent_Snapshot{Snapshot: scanToProto(job)}}
}

func scanStatusEvent(scanID string, value ScanStatus) *scanpb.ScanEvent {
	return &scanpb.ScanEvent{ScanId: scanID, Payload: &scanpb.ScanEvent_Status{Status: scanStatusToProto(value)}}
}

func scanProgressEvent(scanID, data string) *scanpb.ScanEvent {
	return &scanpb.ScanEvent{ScanId: scanID, Payload: &scanpb.ScanEvent_Progress{Progress: &scanpb.ScanProgress{Data: data}}}
}

func scanCompletedEvent(scanID string, result any) *scanpb.ScanEvent {
	encoded, _ := aop.JSONValue(result)
	return &scanpb.ScanEvent{ScanId: scanID, Payload: &scanpb.ScanEvent_Completed{Completed: &scanpb.ScanCompleted{Result: encoded}}}
}

func scanFailedEvent(scanID, message string, canceled bool) *scanpb.ScanEvent {
	return &scanpb.ScanEvent{ScanId: scanID, Payload: &scanpb.ScanEvent_Failed{Failed: &scanpb.ScanFailed{Message: message, Canceled: canceled}}}
}

func scanTerminal(value ScanStatus) bool {
	return value == StatusCompleted || value == StatusFailed || value == StatusCanceled
}

func rejectedSubmitScan(request *scanpb.SubmitScanRequest, code codes.Code, message string) *scanpb.SubmitScanResponse {
	response := &scanpb.SubmitScanResponse{Outcome: &scanpb.SubmitScanResponse_Rejected{Rejected: rejection(code, message)}}
	if request != nil {
		response.RequestId = request.RequestId
	}
	return response
}

func rejectedCancelScan(request *scanpb.CancelScanRequest, code codes.Code, message string) *scanpb.CancelScanResponse {
	response := &scanpb.CancelScanResponse{Outcome: &scanpb.CancelScanResponse_Rejected{Rejected: rejection(code, message)}}
	if request != nil {
		response.RequestId = request.RequestId
	}
	return response
}

func scanRPCError(err error) error {
	switch {
	case errors.Is(err, ErrScanNotFound), errors.Is(err, sql.ErrNoRows):
		return status.Error(codes.NotFound, ErrScanNotFound.Error())
	default:
		return status.Error(codes.Internal, fmt.Sprint(err))
	}
}

func asConnectScanError(err error) error {
	if err == nil {
		return nil
	}
	if grpcStatus, ok := status.FromError(err); ok {
		return connect.NewError(connect.Code(grpcStatus.Code()), errors.New(grpcStatus.Message()))
	}
	return connect.NewError(connect.CodeInternal, err)
}
