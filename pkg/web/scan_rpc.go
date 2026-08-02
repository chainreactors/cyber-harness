package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	errInvalidScanRequest     = errors.New("invalid scan request")
	errScanServiceUnavailable = errors.New("scan service is unavailable")
	errScanReportNotReady     = errors.New("scan report is not ready")
)

type scanServiceCore struct {
	service *Service
}

func newScanServiceCore(service *Service) *scanServiceCore {
	return &scanServiceCore{service: service}
}

func (s *scanServiceCore) SubmitScan(ctx context.Context, request *types.SubmitScanRequest) (*types.SubmitScanResponse, error) {
	if s.service == nil || request == nil || strings.TrimSpace(request.RequestId) == "" {
		return rejectedSubmitScan(request, "INVALID_ARGUMENT", "request_id is required"), nil
	}
	options := request.GetOptions()
	scan, err := s.service.SubmitScan(ctx, request.Target, request.Mode, options.GetVerify(), options.GetSniper(), options.GetDeep())
	if err != nil {
		return rejectedSubmitScan(request, "INVALID_ARGUMENT", err.Error()), nil
	}
	return &types.SubmitScanResponse{RequestId: request.RequestId, Outcome: &types.SubmitScanResponse_Accepted{Accepted: scan}}, nil
}

func (s *scanServiceCore) GetScan(ctx context.Context, request *types.GetScanRequest) (*types.GetScanResponse, error) {
	if s.service == nil || request == nil || strings.TrimSpace(request.ScanId) == "" {
		return nil, fmt.Errorf("%w: scan_id is required", errInvalidScanRequest)
	}
	scan, err := s.service.GetScan(ctx, request.ScanId)
	if err != nil {
		return nil, scanRPCError(err)
	}
	return &types.GetScanResponse{Scan: scan}, nil
}

func (s *scanServiceCore) ListScans(ctx context.Context, _ *types.ListScansRequest) (*types.ListScansResponse, error) {
	if s.service == nil {
		return nil, errScanServiceUnavailable
	}
	scans, err := s.service.ListScans(ctx)
	if err != nil {
		return nil, fmt.Errorf("list scans: %w", err)
	}
	return &types.ListScansResponse{Scans: scans}, nil
}

func (s *scanServiceCore) CancelScan(ctx context.Context, request *types.CancelScanRequest) (*types.CancelScanResponse, error) {
	if s.service == nil || request == nil || strings.TrimSpace(request.RequestId) == "" || strings.TrimSpace(request.ScanId) == "" {
		return rejectedCancelScan(request, "INVALID_ARGUMENT", "request_id and scan_id are required"), nil
	}
	if err := s.service.CancelScan(request.ScanId); err != nil {
		code := "FAILED_PRECONDITION"
		if errors.Is(err, ErrScanNotFound) {
			code = "NOT_FOUND"
		}
		return rejectedCancelScan(request, code, err.Error()), nil
	}
	scan, err := s.service.GetScan(ctx, request.ScanId)
	if err != nil {
		return nil, scanRPCError(err)
	}
	return &types.CancelScanResponse{RequestId: request.RequestId, Outcome: &types.CancelScanResponse_Accepted{Accepted: scan}}, nil
}

func (s *scanServiceCore) WatchScanEvents(request *types.WatchScanEventsRequest, ctx context.Context, send func(*types.ScanEvent) error) error {
	if s.service == nil || request == nil || strings.TrimSpace(request.ScanId) == "" {
		return fmt.Errorf("%w: scan_id is required", errInvalidScanRequest)
	}
	if send == nil {
		return errors.New("scan event sender is unavailable")
	}
	live, snapshotSequence, unsubscribe := s.service.hub.SubscribeScan(request.ScanId)
	defer unsubscribe()
	scan, err := s.service.GetScan(ctx, request.ScanId)
	if err != nil {
		return scanRPCError(err)
	}
	snapshot := scanSnapshot(scan, snapshotSequence)
	if err := send(snapshot); err != nil {
		return err
	}
	if scanTerminal(scan.Status) {
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
			if err := send(event); err != nil {
				return err
			}
			last = event.Sequence
			if event.GetCompleted() != nil || event.GetFailed() != nil {
				return nil
			}
		}
	}
}

func (s *scanServiceCore) GetScanReport(ctx context.Context, request *types.GetScanReportRequest) (*types.GetScanReportResponse, error) {
	if s.service == nil || request == nil || strings.TrimSpace(request.ScanId) == "" {
		return nil, fmt.Errorf("%w: scan_id is required", errInvalidScanRequest)
	}
	markdown, err := s.service.GetReport(ctx, request.ScanId, request.Language)
	if err != nil {
		return nil, scanRPCError(err)
	}
	if markdown == "" {
		return nil, errScanReportNotReady
	}
	return &types.GetScanReportResponse{Markdown: markdown, MediaType: "text/markdown; charset=utf-8"}, nil
}

func scanSnapshot(scan *types.Scan, sequence uint64) *types.ScanEvent {
	return &types.ScanEvent{ScanId: scan.Id, Sequence: sequence, EmittedAt: timestamppb.Now(), Payload: &types.ScanEvent_Snapshot{Snapshot: scan}}
}

func scanStatusEvent(scanID string, value types.ScanStatus) *types.ScanEvent {
	return &types.ScanEvent{ScanId: scanID, Payload: &types.ScanEvent_Status{Status: value}}
}

func scanProgressEvent(scanID, data string) *types.ScanEvent {
	return &types.ScanEvent{ScanId: scanID, Payload: &types.ScanEvent_Progress{Progress: &types.ScanProgress{Data: data}}}
}

func scanCompletedEvent(scanID string) *types.ScanEvent {
	return &types.ScanEvent{ScanId: scanID, Payload: &types.ScanEvent_Completed{Completed: &types.ScanCompleted{}}}
}

func scanFailedEvent(scanID, message string, canceled bool) *types.ScanEvent {
	return &types.ScanEvent{ScanId: scanID, Payload: &types.ScanEvent_Failed{Failed: &types.ScanFailed{Message: message, Canceled: canceled}}}
}

func rejectedSubmitScan(request *types.SubmitScanRequest, code, message string) *types.SubmitScanResponse {
	response := &types.SubmitScanResponse{Outcome: &types.SubmitScanResponse_Rejected{Rejected: rejection(code, message)}}
	if request != nil {
		response.RequestId = request.RequestId
	}
	return response
}

func rejectedCancelScan(request *types.CancelScanRequest, code, message string) *types.CancelScanResponse {
	response := &types.CancelScanResponse{Outcome: &types.CancelScanResponse_Rejected{Rejected: rejection(code, message)}}
	if request != nil {
		response.RequestId = request.RequestId
	}
	return response
}

func scanRPCError(err error) error {
	switch {
	case errors.Is(err, ErrScanNotFound), errors.Is(err, sql.ErrNoRows):
		return ErrScanNotFound
	default:
		return fmt.Errorf("scan service: %w", err)
	}
}

func asConnectScanError(err error) error {
	if err == nil {
		return nil
	}
	code := connect.CodeInternal
	switch {
	case errors.Is(err, errInvalidScanRequest):
		code = connect.CodeInvalidArgument
	case errors.Is(err, ErrScanNotFound), errors.Is(err, sql.ErrNoRows):
		code = connect.CodeNotFound
	case errors.Is(err, errScanServiceUnavailable):
		code = connect.CodeUnavailable
	case errors.Is(err, errScanReportNotReady):
		code = connect.CodeFailedPrecondition
	case errors.Is(err, context.Canceled):
		code = connect.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = connect.CodeDeadlineExceeded
	}
	return connect.NewError(code, err)
}
