package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	aop "github.com/chainreactors/cyber/aop"
	scanpb "github.com/chainreactors/cyber/pkg/web/scan"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrScanUnavailable   = errors.New("no scan execution nodes connected; connect an external agent or start Web without --no-agent")
	ErrScanNotFound      = errors.New("scan not found")
	ErrScanNotCancelable = errors.New("scan cannot be canceled")
	// ErrScanConsoleDisabled rejects scan work on a host built without the
	// scan console, including session scan bindings.
	ErrScanConsoleDisabled = errors.New("scan console is disabled")
)

type ScanBackend interface {
	SubmitScan(context.Context, string, string, bool, bool, bool) (*scanpb.Scan, error)
	GetScan(context.Context, string) (*scanpb.Scan, error)
	ListScans(context.Context) ([]*scanpb.Scan, error)
	CancelScan(string) error
}

type ScanEvents interface {
	SubscribeScan(string) (<-chan *scanpb.ScanEvent, uint64, func())
}

type Scans struct {
	backend ScanBackend
	events  ScanEvents
}

func NewScans(backend ScanBackend, events ScanEvents) *Scans {
	return &Scans{backend: backend, events: events}
}

func (s *Scans) SubmitScan(ctx context.Context, request *scanpb.SubmitScanRequest) (*scanpb.SubmitScanResponse, error) {
	if s == nil || s.backend == nil || request == nil || strings.TrimSpace(request.RequestId) == "" {
		return rejectedSubmitScan(request, "INVALID_ARGUMENT", "request_id is required"), nil
	}
	options := request.GetOptions()
	scan, err := s.backend.SubmitScan(ctx, request.Target, request.Mode, options.GetVerify(), options.GetSniper(), options.GetDeep())
	if err != nil {
		code := "INVALID_ARGUMENT"
		if errors.Is(err, ErrScanUnavailable) || errors.Is(err, ErrScanConsoleDisabled) {
			code = "FAILED_PRECONDITION"
		}
		return rejectedSubmitScan(request, code, err.Error()), nil
	}
	return &scanpb.SubmitScanResponse{RequestId: request.RequestId, Outcome: &scanpb.SubmitScanResponse_Accepted{Accepted: scan}}, nil
}

func (s *Scans) GetScan(ctx context.Context, request *scanpb.GetScanRequest) (*scanpb.GetScanResponse, error) {
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
	return &scanpb.GetScanResponse{Scan: scan}, nil
}

func (s *Scans) ListScans(ctx context.Context, _ *scanpb.ListScansRequest) (*scanpb.ListScansResponse, error) {
	if s == nil || s.backend == nil {
		return nil, Errorf(CodeUnavailable, "scan service is unavailable")
	}
	scans, err := s.backend.ListScans(ctx)
	if err != nil {
		return nil, fmt.Errorf("list scans: %w", err)
	}
	return &scanpb.ListScansResponse{Scans: scans}, nil
}

func (s *Scans) CancelScan(ctx context.Context, request *scanpb.CancelScanRequest) (*scanpb.CancelScanResponse, error) {
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
	return &scanpb.CancelScanResponse{RequestId: request.RequestId, Outcome: &scanpb.CancelScanResponse_Accepted{Accepted: scan}}, nil
}

func (s *Scans) WatchScanEvents(request *scanpb.WatchScanEventsRequest, ctx context.Context, send func(*scanpb.ScanEvent) error) error {
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

func ScanTerminal(status scanpb.ScanStatus) bool {
	return status == scanpb.ScanStatus_SCAN_STATUS_COMPLETED || status == scanpb.ScanStatus_SCAN_STATUS_FAILED || status == scanpb.ScanStatus_SCAN_STATUS_CANCELED
}

func ScanSnapshot(scan *scanpb.Scan, sequence uint64) *scanpb.ScanEvent {
	return &scanpb.ScanEvent{ScanId: scan.GetId(), Sequence: sequence, EmittedAt: timestamppb.Now(), Payload: &scanpb.ScanEvent_Snapshot{Snapshot: scan}}
}

func ScanStatusEvent(scanID string, status scanpb.ScanStatus) *scanpb.ScanEvent {
	return &scanpb.ScanEvent{ScanId: scanID, Payload: &scanpb.ScanEvent_Status{Status: status}}
}

func ScanProgressEvent(scanID, data string) *scanpb.ScanEvent {
	return &scanpb.ScanEvent{ScanId: scanID, Payload: &scanpb.ScanEvent_Progress{Progress: &scanpb.ScanProgress{Data: data}}}
}

func ScanCompletedEvent(scanID string) *scanpb.ScanEvent {
	return &scanpb.ScanEvent{ScanId: scanID, Payload: &scanpb.ScanEvent_Completed{Completed: &scanpb.ScanCompleted{}}}
}

func ScanFailedEvent(scanID, message string, canceled bool) *scanpb.ScanEvent {
	return &scanpb.ScanEvent{ScanId: scanID, Payload: &scanpb.ScanEvent_Failed{Failed: &scanpb.ScanFailed{Message: message, Canceled: canceled}}}
}

func rejectedSubmitScan(request *scanpb.SubmitScanRequest, code, message string) *scanpb.SubmitScanResponse {
	response := &scanpb.SubmitScanResponse{Outcome: &scanpb.SubmitScanResponse_Rejected{Rejected: rejection(code, message)}}
	if request != nil {
		response.RequestId = request.RequestId
	}
	return response
}

func rejectedCancelScan(request *scanpb.CancelScanRequest, code, message string) *scanpb.CancelScanResponse {
	response := &scanpb.CancelScanResponse{Outcome: &scanpb.CancelScanResponse_Rejected{Rejected: rejection(code, message)}}
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
	if errors.Is(err, ErrScanConsoleDisabled) {
		return NewError(CodeFailedPrecondition, err)
	}
	return fmt.Errorf("scan service: %w", err)
}
