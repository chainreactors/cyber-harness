package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"runtime/debug"
	"strings"

	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/pkg/output"
	managementapi "github.com/chainreactors/cyber/pkg/web/api"
	scanpb "github.com/chainreactors/cyber/pkg/web/scan"
	"google.golang.org/protobuf/proto"
)

var (
	ErrScanUnavailable   = managementapi.ErrScanUnavailable
	ErrScanNotFound      = managementapi.ErrScanNotFound
	ErrScanNotCancelable = managementapi.ErrScanNotCancelable
	// ErrScanConsoleDisabled rejects scan operations on a host built without
	// the scan console (ServiceConfig.Scans == nil).
	ErrScanConsoleDisabled = managementapi.ErrScanConsoleDisabled
)

// scansEnabled reports whether the service mounts the scan console.
func (s *Service) scansEnabled() bool { return s.sem != nil }

// scanStatusToDB maps the proto enum to the string stored in scans.status.
func scanStatusToDB(value scanpb.ScanStatus) string {
	switch value {
	case scanpb.ScanStatus_SCAN_STATUS_RUNNING:
		return "running"
	case scanpb.ScanStatus_SCAN_STATUS_COMPLETED:
		return "completed"
	case scanpb.ScanStatus_SCAN_STATUS_FAILED:
		return "failed"
	case scanpb.ScanStatus_SCAN_STATUS_CANCELED:
		return "canceled"
	default:
		return "queued"
	}
}

func (s *Service) SubmitScan(ctx context.Context, target, mode string, options *scanpb.ScanOptions) (*scanpb.Scan, error) {
	if !s.scansEnabled() {
		return nil, ErrScanConsoleDisabled
	}
	workCtx, admitted := s.beginWork()
	if !admitted {
		return nil, fmt.Errorf("web service is closing")
	}
	defer s.work.Done()
	target, err := ValidateTarget(target)
	if err != nil {
		return nil, err
	}
	mode, err = ValidateMode(mode)
	if err != nil {
		return nil, err
	}

	if s.agents == nil || s.agents.Count() == 0 {
		return nil, ErrScanUnavailable
	}

	now := nowProto()
	scan := &scanpb.Scan{
		Id:        generateID(),
		Target:    target,
		Mode:      mode,
		Options:   proto.CloneOf(options),
		Status:    scanpb.ScanStatus_SCAN_STATUS_QUEUED,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.store.Create(ctx, scan); err != nil {
		return nil, fmt.Errorf("store create: %w", err)
	}

	runCtx, cancel := context.WithCancel(workCtx)
	s.mu.Lock()
	s.cancels[scan.Id] = cancel
	s.mu.Unlock()
	s.work.Add(1)
	go func() { //nolint:gosec // G118: background scan intentionally outlives the request
		defer s.work.Done()
		defer cancel()
		s.runScan(runCtx, scan.Id)
	}()

	return scan, nil
}

func (s *Service) GetScan(ctx context.Context, id string) (*scanpb.Scan, error) {
	if !s.scansEnabled() {
		return nil, ErrScanConsoleDisabled
	}
	scan, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return scan, nil
}

func (s *Service) ListScans(ctx context.Context) ([]*scanpb.Scan, error) {
	scans, err := s.store.List(ctx, 100)
	if err != nil {
		return nil, err
	}
	return scans, nil
}

func (s *Service) CancelScan(id string) error {
	if !s.scansEnabled() {
		return ErrScanConsoleDisabled
	}
	ctx := context.Background()
	scan, err := s.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrScanNotFound, id)
		}
		return err
	}
	if scan.Status == scanpb.ScanStatus_SCAN_STATUS_CANCELED {
		return nil
	}
	if scan.Status != scanpb.ScanStatus_SCAN_STATUS_RUNNING && scan.Status != scanpb.ScanStatus_SCAN_STATUS_QUEUED {
		return fmt.Errorf("%w: scan %s is %s", ErrScanNotCancelable, id, scanStatusToDB(scan.Status))
	}
	scan.Status = scanpb.ScanStatus_SCAN_STATUS_CANCELED
	scan.UpdatedAt = nowProto()
	changed, err := s.store.TransitionScan(ctx, scan, scanpb.ScanStatus_SCAN_STATUS_RUNNING, scanpb.ScanStatus_SCAN_STATUS_QUEUED)
	if err != nil {
		return err
	}
	if !changed {
		current, err := s.store.Get(ctx, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrScanNotFound, id)
			}
			return err
		}
		if current.Status == scanpb.ScanStatus_SCAN_STATUS_CANCELED {
			return nil
		}
		return fmt.Errorf("%w: scan %s is %s", ErrScanNotCancelable, id, scanStatusToDB(current.Status))
	}

	s.mu.Lock()
	cancel := s.cancels[id]
	nodeID := s.scanNodeIDs[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.hub.BroadcastScan(managementapi.ScanFailedEvent(id, "scan canceled", true), true)
	s.broadcastScanStatus(id, scanpb.ScanStatus_SCAN_STATUS_CANCELED)
	if nodeID != "" && s.agents != nil {
		_ = s.agents.CancelTask(nodeID, id, "")
	}
	return nil
}

func (s *Service) runScan(runCtx context.Context, scanID string) {
	defer func() {
		s.mu.Lock()
		delete(s.cancels, scanID)
		delete(s.scanNodeIDs, scanID)
		s.mu.Unlock()
	}()
	defer func() {
		if recovered := recover(); recovered != nil {
			telemetry.GlobalLogs().Errorf("scan panic scan_id=%s panic=%v\n%s", scanID, recovered, debug.Stack())
			if scan, err := s.store.Get(context.Background(), scanID); err == nil {
				_, _ = s.failScan(scan, "scan failed unexpectedly")
			}
		}
	}()

	select {
	case s.sem <- struct{}{}:
	case <-runCtx.Done():
		return
	}
	defer func() { <-s.sem }()

	ctx, cancel := context.WithTimeout(runCtx, s.timeout)
	defer cancel()

	scan, err := s.store.Get(ctx, scanID)
	if err != nil {
		return
	}
	scan.Status = scanpb.ScanStatus_SCAN_STATUS_RUNNING
	scan.UpdatedAt = nowProto()
	changed, err := s.store.TransitionScan(context.Background(), scan, scanpb.ScanStatus_SCAN_STATUS_QUEUED)
	if err != nil || !changed {
		return
	}

	s.hub.BroadcastScan(managementapi.ScanStatusEvent(scanID, scanpb.ScanStatus_SCAN_STATUS_RUNNING), false)

	// The Web hub delegates scans to nodes; it does not own scanning engines.
	s.runScanViaAgent(ctx, scan)
}

func (s *Service) runScanViaAgent(ctx context.Context, scan *scanpb.Scan) {
	var agent *remoteAgent
	if s.agents != nil {
		agent = s.agents.Pick()
	}
	if agent == nil {
		_, _ = s.failScan(scan, ErrScanUnavailable.Error())
		return
	}
	s.mu.Lock()
	s.scanNodeIDs[scan.Id] = agent.NodeID()
	s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		s.finishScanContext(scan, err)
		return
	}

	cmd := "scan " + strings.Join(scanArgsForScan(scan), " ")
	args, _ := aop.JSONValue(map[string]any{"command": cmd})
	resultCh, err := s.agents.DispatchToolCall(agent.NodeID(), scan.Id, &aop.ToolCall{
		Id: scan.Id, Name: "bash", Kind: "function", Arguments: args,
	})
	if err != nil {
		_, _ = s.failScan(scan, err.Error())
		return
	}

	// Progress lines stream to the SSE hub as tool.data events while the scan
	// runs; the terminal tool.result carries the full text and the structured
	// scan result in its details.
	var res taskResult
	var ok bool
	select {
	case <-ctx.Done():
		_ = s.agents.CancelTask(agent.NodeID(), scan.Id, "")
		s.finishScanContext(scan, ctx.Err())
		return
	case res, ok = <-resultCh:
	}
	if ctx.Err() != nil {
		_ = s.agents.CancelTask(agent.NodeID(), scan.Id, "")
		s.finishScanContext(scan, ctx.Err())
		return
	}
	if !ok {
		_, _ = s.failScan(scan, "agent disconnected")
		return
	}
	if res.Err != "" {
		_, _ = s.failScan(scan, res.Err)
		return
	}
	if progress := lastOutputLine(res.Output); progress != "" {
		scan.Progress = progress
	}

	_, _ = s.completeScan(context.Background(), scan)
}

func (s *Service) finishScanContext(scan *scanpb.Scan, err error) {
	if err == nil {
		return
	}
	if err == context.DeadlineExceeded {
		_, _ = s.failScan(scan, "scan timed out")
		return
	}
	next := proto.CloneOf(scan)
	next.Status = scanpb.ScanStatus_SCAN_STATUS_CANCELED
	next.UpdatedAt = nowProto()
	if changed, _ := s.store.TransitionScan(context.Background(), next, scanpb.ScanStatus_SCAN_STATUS_QUEUED, scanpb.ScanStatus_SCAN_STATUS_RUNNING); changed {
		s.broadcastScanStatus(scan.Id, next.Status)
	}
}

func (s *Service) completeScan(ctx context.Context, scan *scanpb.Scan) (bool, error) {
	next := proto.CloneOf(scan)
	next.Status = scanpb.ScanStatus_SCAN_STATUS_COMPLETED
	next.Error = ""
	next.UpdatedAt = nowProto()
	changed, err := s.store.TransitionScan(ctx, next, scanpb.ScanStatus_SCAN_STATUS_RUNNING)
	if err != nil || !changed {
		return changed, err
	}
	proto.Merge(scan, next)
	s.hub.BroadcastScan(managementapi.ScanCompletedEvent(scan.Id), true)
	s.broadcastScanComplete(scan.Id)
	return true, nil
}

func (s *Service) failScan(scan *scanpb.Scan, errMsg string) (bool, error) {
	next := proto.CloneOf(scan)
	next.Status = scanpb.ScanStatus_SCAN_STATUS_FAILED
	next.Error = errMsg
	next.UpdatedAt = nowProto()
	changed, err := s.store.TransitionScan(context.Background(), next, scanpb.ScanStatus_SCAN_STATUS_QUEUED, scanpb.ScanStatus_SCAN_STATUS_RUNNING)
	if err != nil || !changed {
		return changed, err
	}
	proto.Merge(scan, next)
	s.hub.BroadcastScan(managementapi.ScanFailedEvent(scan.Id, errMsg, false), true)
	s.broadcastScanStatus(scan.Id, scanpb.ScanStatus_SCAN_STATUS_FAILED)
	return true, nil
}

func scanArgsForScan(scan *scanpb.Scan) []string {
	args := []string{"-i", scan.Target, "--mode", scan.Mode}
	options := scan.GetOptions()
	if options != nil && options.Verify != nil {
		if options.GetVerify() {
			args = append(args, "--verify=on")
		} else {
			args = append(args, "--verify=off")
		}
	}
	if options.GetSniper() {
		args = append(args, "--sniper")
	}
	return args
}

func lastOutputLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(output.StripANSI(lines[i]))
		if line != "" {
			return line
		}
	}
	return ""
}

func ValidateTarget(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("target is required")
	}

	if strings.Contains(raw, ",") || strings.Contains(raw, " ") {
		return "", fmt.Errorf("only a single target is allowed")
	}

	if idx := strings.Index(raw, "/"); idx >= 0 {
		prefix := raw[:idx]
		if net.ParseIP(prefix) != nil {
			return "", fmt.Errorf("CIDR ranges are not allowed; provide a single IP or URL")
		}
		if host, _, err := net.SplitHostPort(prefix); err == nil && net.ParseIP(host) != nil {
			return "", fmt.Errorf("CIDR ranges are not allowed; provide a single IP or URL")
		}
	}

	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Hostname() == "" {
			return "", fmt.Errorf("invalid URL: %s", raw)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", fmt.Errorf("only http and https URLs are allowed")
		}
		return raw, nil
	}

	if host, _, err := net.SplitHostPort(raw); err == nil {
		if net.ParseIP(host) != nil {
			return raw, nil
		}
		return raw, nil
	}

	if net.ParseIP(raw) != nil {
		return raw, nil
	}

	if isValidHostname(raw) {
		return raw, nil
	}

	return "", fmt.Errorf("invalid target: %s (expected IP, IP:port, hostname, or URL)", raw)
}

func ValidateMode(mode string) (string, error) {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		return "quick", nil
	}
	switch mode {
	case "quick", "full":
		return mode, nil
	default:
		return "", fmt.Errorf("invalid mode %q: must be quick or full", mode)
	}
}

func isValidHostname(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	if !strings.Contains(s, ".") {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '.') {
			return false
		}
	}
	return true
}
