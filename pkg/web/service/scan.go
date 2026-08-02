package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/commands"
	types "github.com/chainreactors/aiscan/pkg/types"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
	"google.golang.org/protobuf/proto"
)

var (
	ErrScanNotFound      = managementapi.ErrScanNotFound
	ErrScanNotCancelable = managementapi.ErrScanNotCancelable
)

// scanStatusToDB maps the proto enum to the string stored in scans.status.
func scanStatusToDB(value types.ScanStatus) string {
	switch value {
	case types.ScanStatus_SCAN_STATUS_RUNNING:
		return "running"
	case types.ScanStatus_SCAN_STATUS_COMPLETED:
		return "completed"
	case types.ScanStatus_SCAN_STATUS_FAILED:
		return "failed"
	case types.ScanStatus_SCAN_STATUS_CANCELED:
		return "canceled"
	default:
		return "queued"
	}
}

func (s *Service) SubmitScan(ctx context.Context, target, mode string, verify, sniper, deep bool) (*types.Scan, error) {
	target, err := ValidateTarget(target)
	if err != nil {
		return nil, err
	}
	mode, err = ValidateMode(mode)
	if err != nil {
		return nil, err
	}
	if (verify || sniper || deep) && !s.aiAvailable() {
		return nil, fmt.Errorf("selected analysis options require an LLM provider")
	}

	now := nowProto()
	scan := &types.Scan{
		Id:        generateID(),
		Target:    target,
		Mode:      mode,
		Options:   &types.ScanOptions{Verify: verify, Sniper: sniper, Deep: deep},
		Status:    types.ScanStatus_SCAN_STATUS_QUEUED,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.store.Create(ctx, scan); err != nil {
		return nil, fmt.Errorf("store create: %w", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[scan.Id] = cancel
	s.mu.Unlock()
	go func() { //nolint:gosec // G118: background scan intentionally outlives the request
		defer cancel()
		s.runScan(runCtx, scan.Id)
	}()

	return scan, nil
}

func (s *Service) GetScan(ctx context.Context, id string) (*types.Scan, error) {
	scan, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return scan, nil
}

func (s *Service) ListScans(ctx context.Context) ([]*types.Scan, error) {
	scans, err := s.store.List(ctx, 100)
	if err != nil {
		return nil, err
	}
	return scans, nil
}

func (s *Service) CancelScan(id string) error {
	ctx := context.Background()
	scan, err := s.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrScanNotFound, id)
		}
		return err
	}
	if scan.Status == types.ScanStatus_SCAN_STATUS_CANCELED {
		return nil
	}
	if scan.Status != types.ScanStatus_SCAN_STATUS_RUNNING && scan.Status != types.ScanStatus_SCAN_STATUS_QUEUED {
		return fmt.Errorf("%w: scan %s is %s", ErrScanNotCancelable, id, scanStatusToDB(scan.Status))
	}
	scan.Status = types.ScanStatus_SCAN_STATUS_CANCELED
	scan.UpdatedAt = nowProto()
	changed, err := s.store.TransitionScan(ctx, scan, types.ScanStatus_SCAN_STATUS_RUNNING, types.ScanStatus_SCAN_STATUS_QUEUED)
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
		if current.Status == types.ScanStatus_SCAN_STATUS_CANCELED {
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
	if nodeID != "" && s.agents != nil {
		_ = s.agents.CancelTask(nodeID, id)
	}
	return nil
}

// GetReport returns the report frozen when the scan completed. Canonical scan
// artifacts live in the libcstx SCO store, not inside Scan.
func (s *Service) GetReport(ctx context.Context, id, lang string) (string, error) {
	_ = lang
	scan, err := s.GetScan(ctx, id)
	if err != nil {
		return "", err
	}
	return scan.Report, nil
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
			if scan, err := s.store.Get(context.Background(), scanID); err == nil {
				_, _ = s.failScan(scan, fmt.Sprintf("scan runtime panic: %v", recovered))
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
	scan.Status = types.ScanStatus_SCAN_STATUS_RUNNING
	scan.UpdatedAt = nowProto()
	changed, err := s.store.TransitionScan(context.Background(), scan, types.ScanStatus_SCAN_STATUS_QUEUED)
	if err != nil || !changed {
		return
	}

	s.hub.BroadcastScan(managementapi.ScanStatusEvent(scanID, types.ScanStatus_SCAN_STATUS_RUNNING), false)

	// Try agent dispatch first, fall back to local execution.
	if s.agents != nil && s.agents.Count() > 0 {
		s.runScanViaAgent(ctx, scan)
		return
	}
	s.runScanLocally(ctx, scan)
}

func (s *Service) runScanViaAgent(ctx context.Context, scan *types.Scan) {
	agent := s.agents.Pick()
	if agent == nil {
		_, _ = s.failScan(scan, "no agents available")
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
		_ = s.agents.CancelTask(agent.NodeID(), scan.Id)
		s.finishScanContext(scan, ctx.Err())
		return
	case res, ok = <-resultCh:
	}
	if ctx.Err() != nil {
		_ = s.agents.CancelTask(agent.NodeID(), scan.Id)
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

func (s *Service) runScanLocally(ctx context.Context, scan *types.Scan) {
	ctx = output.ContextWithCallID(ctx, scan.Id)
	streamWriter := &scanStreamWriter{
		hub:    s.hub,
		scanID: scan.Id,
		store:  s.store,
		scan:   scan,
		ctx:    ctx,
	}

	args := scanArgsForScan(scan)
	_, err := s.executeScan(ctx, args, streamWriter)
	if err != nil {
		s.finishScanContext(scan, ctx.Err())
		if ctx.Err() == nil {
			_, _ = s.failScan(scan, err.Error())
		}
		return
	}
	if streamWriter.scan != nil {
		scan = streamWriter.scan
	}
	if ctx.Err() != nil {
		s.finishScanContext(scan, ctx.Err())
		return
	}

	_, _ = s.completeScan(context.Background(), scan)
}

func (s *Service) finishScanContext(scan *types.Scan, err error) {
	if err == nil {
		return
	}
	if err == context.DeadlineExceeded {
		_, _ = s.failScan(scan, "scan timed out")
		return
	}
	next := proto.CloneOf(scan)
	next.Status = types.ScanStatus_SCAN_STATUS_CANCELED
	next.UpdatedAt = nowProto()
	_, _ = s.store.TransitionScan(context.Background(), next, types.ScanStatus_SCAN_STATUS_QUEUED, types.ScanStatus_SCAN_STATUS_RUNNING)
}

func (s *Service) completeScan(ctx context.Context, scan *types.Scan) (bool, error) {
	nodes, err := s.store.ListSCONodesByScanID(ctx, scan.Id, "", 100000)
	if err != nil {
		return false, fmt.Errorf("load scan SCO facts: %w", err)
	}
	next := proto.CloneOf(scan)
	next.Status = types.ScanStatus_SCAN_STATUS_COMPLETED
	next.Report = managementapi.BuildMarkdownReport(scan.Target, scan.Mode, nodes, managementapi.DefaultReportLang)
	next.Error = ""
	next.UpdatedAt = nowProto()
	changed, err := s.store.TransitionScan(ctx, next, types.ScanStatus_SCAN_STATUS_RUNNING)
	if err != nil || !changed {
		return changed, err
	}
	proto.Merge(scan, next)
	s.hub.BroadcastScan(managementapi.ScanCompletedEvent(scan.Id), true)
	s.broadcastScanComplete(scan.Id)
	return true, nil
}

func (s *Service) failScan(scan *types.Scan, errMsg string) (bool, error) {
	next := proto.CloneOf(scan)
	next.Status = types.ScanStatus_SCAN_STATUS_FAILED
	next.Error = errMsg
	next.UpdatedAt = nowProto()
	changed, err := s.store.TransitionScan(context.Background(), next, types.ScanStatus_SCAN_STATUS_QUEUED, types.ScanStatus_SCAN_STATUS_RUNNING)
	if err != nil || !changed {
		return changed, err
	}
	proto.Merge(scan, next)
	s.hub.BroadcastScan(managementapi.ScanFailedEvent(scan.Id, errMsg, false), true)
	return true, nil
}

func scanArgsForScan(scan *types.Scan) []string {
	args := []string{"-i", scan.Target, "--mode", scan.Mode}
	options := scan.GetOptions()
	if options.GetVerify() {
		args = append(args, "--verify=high")
	}
	if options.GetSniper() {
		args = append(args, "--sniper")
	}
	if options.GetDeep() {
		args = append(args, "--deep")
	}
	return args
}

func (s *Service) executeScan(ctx context.Context, args []string, stream io.Writer) (string, error) {
	app, release := s.acquireApp()
	defer release()
	if app == nil || app.Commands == nil {
		return "", fmt.Errorf("aiscan runtime is not ready")
	}
	tool, ok := app.Commands.GetTool("bash")
	if !ok {
		return "", fmt.Errorf("bash tool is not registered")
	}
	bash, ok := tool.(*commands.BashTool)
	if !ok {
		return "", fmt.Errorf("registered bash tool has unexpected type")
	}
	var text strings.Builder
	if _, err := bash.RunForeground(ctx, commands.JoinCommandLine("scan", args), commands.BashExecOptions{
		OnOutput: func(data []byte) {
			_, _ = text.Write(data)
			if stream != nil {
				_, _ = stream.Write(data)
			}
		},
	}); err != nil {
		return text.String(), err
	}
	return text.String(), nil
}

type scanStreamWriter struct {
	hub    *Hub
	scanID string
	store  *SQLiteStore
	scan   *types.Scan
	ctx    context.Context
	buf    []byte
}

func (w *scanStreamWriter) Write(p []byte) (int, error) {
	if w.ctx != nil {
		select {
		case <-w.ctx.Done():
			return 0, w.ctx.Err()
		default:
		}
	}
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := string(w.buf[:idx])
		w.buf = w.buf[idx+1:]

		line = output.StripANSI(line)
		if line == "" {
			continue
		}

		fmt.Fprintf(os.Stderr, "[scan:%s] %s\n", w.scanID, line)

		current, err := w.store.Get(context.Background(), w.scanID)
		if err != nil {
			return 0, err
		}
		if current.Status == types.ScanStatus_SCAN_STATUS_CANCELED {
			return 0, context.Canceled
		}
		current.Progress = line
		current.UpdatedAt = nowProto()
		changed, err := w.store.TransitionScan(context.Background(), current, types.ScanStatus_SCAN_STATUS_RUNNING)
		if err != nil {
			return 0, err
		}
		if !changed {
			return 0, context.Canceled
		}
		w.scan = current

		w.hub.BroadcastScan(managementapi.ScanProgressEvent(w.scanID, line), false)
	}
	return len(p), nil
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
