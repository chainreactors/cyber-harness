package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrScanNotFound      = errors.New("scan not found")
	ErrScanNotCancelable = errors.New("scan cannot be canceled")
)

type ScanBackend interface {
	SubmitScan(context.Context, string, string, bool, bool, bool) (*types.Scan, error)
	GetScan(context.Context, string) (*types.Scan, error)
	ListScans(context.Context) ([]*types.Scan, error)
	CancelScan(string) error
	GetReport(context.Context, string, string) (string, error)
}

type ScanEvents interface {
	SubscribeScan(string) (<-chan *types.ScanEvent, uint64, func())
}

type Scans struct {
	backend ScanBackend
	events  ScanEvents
}

func NewScans(backend ScanBackend, events ScanEvents) *Scans {
	return &Scans{backend: backend, events: events}
}

func (s *Scans) SubmitScan(ctx context.Context, request *types.SubmitScanRequest) (*types.SubmitScanResponse, error) {
	if s == nil || s.backend == nil || request == nil || strings.TrimSpace(request.RequestId) == "" {
		return rejectedSubmitScan(request, "INVALID_ARGUMENT", "request_id is required"), nil
	}
	options := request.GetOptions()
	scan, err := s.backend.SubmitScan(ctx, request.Target, request.Mode, options.GetVerify(), options.GetSniper(), options.GetDeep())
	if err != nil {
		return rejectedSubmitScan(request, "INVALID_ARGUMENT", err.Error()), nil
	}
	return &types.SubmitScanResponse{RequestId: request.RequestId, Outcome: &types.SubmitScanResponse_Accepted{Accepted: scan}}, nil
}

func (s *Scans) GetScan(ctx context.Context, request *types.GetScanRequest) (*types.GetScanResponse, error) {
	if s == nil || s.backend == nil {
		return nil, Errorf(CodeUnavailable, "scan service is unavailable")
	}
	if request == nil || strings.TrimSpace(request.ScanId) == "" {
		return nil, Errorf(CodeInvalidArgument, "scan_id is required")
	}
	scan, err := s.backend.GetScan(ctx, request.ScanId)
	if err != nil {
		return nil, scanError(err)
	}
	return &types.GetScanResponse{Scan: scan}, nil
}

func (s *Scans) ListScans(ctx context.Context, _ *types.ListScansRequest) (*types.ListScansResponse, error) {
	if s == nil || s.backend == nil {
		return nil, Errorf(CodeUnavailable, "scan service is unavailable")
	}
	scans, err := s.backend.ListScans(ctx)
	if err != nil {
		return nil, fmt.Errorf("list scans: %w", err)
	}
	return &types.ListScansResponse{Scans: scans}, nil
}

func (s *Scans) CancelScan(ctx context.Context, request *types.CancelScanRequest) (*types.CancelScanResponse, error) {
	if s == nil || s.backend == nil || request == nil || strings.TrimSpace(request.RequestId) == "" || strings.TrimSpace(request.ScanId) == "" {
		return rejectedCancelScan(request, "INVALID_ARGUMENT", "request_id and scan_id are required"), nil
	}
	if err := s.backend.CancelScan(request.ScanId); err != nil {
		code := "FAILED_PRECONDITION"
		if errors.Is(err, ErrScanNotFound) {
			code = "NOT_FOUND"
		}
		return rejectedCancelScan(request, code, err.Error()), nil
	}
	scan, err := s.backend.GetScan(ctx, request.ScanId)
	if err != nil {
		return nil, scanError(err)
	}
	return &types.CancelScanResponse{RequestId: request.RequestId, Outcome: &types.CancelScanResponse_Accepted{Accepted: scan}}, nil
}

func (s *Scans) GetScanReport(ctx context.Context, request *types.GetScanReportRequest) (*types.GetScanReportResponse, error) {
	if s == nil || s.backend == nil {
		return nil, Errorf(CodeUnavailable, "scan service is unavailable")
	}
	if request == nil || strings.TrimSpace(request.ScanId) == "" {
		return nil, Errorf(CodeInvalidArgument, "scan_id is required")
	}
	markdown, err := s.backend.GetReport(ctx, request.ScanId, request.Language)
	if err != nil {
		return nil, scanError(err)
	}
	if markdown == "" {
		return nil, Errorf(CodeFailedPrecondition, "scan report is not ready")
	}
	return &types.GetScanReportResponse{Markdown: markdown, MediaType: "text/markdown; charset=utf-8"}, nil
}

func (s *Scans) WatchScanEvents(request *types.WatchScanEventsRequest, ctx context.Context, send func(*types.ScanEvent) error) error {
	if s == nil || s.backend == nil || s.events == nil {
		return Errorf(CodeUnavailable, "scan service is unavailable")
	}
	if request == nil || strings.TrimSpace(request.ScanId) == "" {
		return Errorf(CodeInvalidArgument, "scan_id is required")
	}
	if send == nil {
		return Errorf(CodeInvalidArgument, "scan event sender is unavailable")
	}
	live, sequence, unsubscribe := s.events.SubscribeScan(request.ScanId)
	defer unsubscribe()
	scan, err := s.backend.GetScan(ctx, request.ScanId)
	if err != nil {
		return scanError(err)
	}
	snapshot := ScanSnapshot(scan, sequence)
	if err := send(snapshot); err != nil {
		return err
	}
	if ScanTerminal(scan.Status) {
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

func ScanTerminal(status types.ScanStatus) bool {
	return status == types.ScanStatus_SCAN_STATUS_COMPLETED || status == types.ScanStatus_SCAN_STATUS_FAILED || status == types.ScanStatus_SCAN_STATUS_CANCELED
}

func ScanSnapshot(scan *types.Scan, sequence uint64) *types.ScanEvent {
	return &types.ScanEvent{ScanId: scan.GetId(), Sequence: sequence, EmittedAt: timestamppb.Now(), Payload: &types.ScanEvent_Snapshot{Snapshot: scan}}
}

func ScanStatusEvent(scanID string, status types.ScanStatus) *types.ScanEvent {
	return &types.ScanEvent{ScanId: scanID, Payload: &types.ScanEvent_Status{Status: status}}
}

func ScanProgressEvent(scanID, data string) *types.ScanEvent {
	return &types.ScanEvent{ScanId: scanID, Payload: &types.ScanEvent_Progress{Progress: &types.ScanProgress{Data: data}}}
}

func ScanCompletedEvent(scanID string) *types.ScanEvent {
	return &types.ScanEvent{ScanId: scanID, Payload: &types.ScanEvent_Completed{Completed: &types.ScanCompleted{}}}
}

func ScanFailedEvent(scanID, message string, canceled bool) *types.ScanEvent {
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

func rejection(code, message string) *aop.Rejection {
	return &aop.Rejection{Code: code, Message: message}
}

func scanError(err error) error {
	if errors.Is(err, ErrScanNotFound) || errors.Is(err, sql.ErrNoRows) {
		return NewError(CodeNotFound, ErrScanNotFound)
	}
	return fmt.Errorf("scan service: %w", err)
}
