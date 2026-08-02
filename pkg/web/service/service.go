package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	filepb "github.com/chainreactors/aiscan/aop/file"
	"github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/runner"
	"github.com/chainreactors/aiscan/pkg/tui"
	types "github.com/chainreactors/aiscan/pkg/types"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ServiceConfig struct {
	Store         *SQLiteStore
	App           *runner.App
	ConfigStore   ConfigStore
	AppFactory    func(ctx context.Context, prepared *PreparedConfig) (*runner.App, error)
	AgentPool     *AgentPool
	MaxConcurrent int
	ScanTimeout   time.Duration
	AccessKey     string
}

type Service struct {
	store   *SQLiteStore
	appMu   sync.Mutex
	app     *managedApp
	api     *managementapi.API
	auth    *Auth
	agents  *AgentPool
	hub     *Hub
	sem     chan struct{}
	timeout time.Duration

	mu           sync.Mutex
	cancels      map[string]context.CancelFunc
	scanNodeIDs  map[string]string
	taskSessions map[string]string // taskID → sessionID
	taskNodeIDs  map[string]string // taskID → nodeID
	taskCanceled map[string]bool

	eventMu    sync.Mutex
	sessionSeq map[string]uint64
	endedTurns map[string]bool
}

type managedApp struct {
	app     *runner.App
	refs    int
	retired bool
	closed  bool
}

func NewService(cfg ServiceConfig) *Service {
	maxConcurrent := cfg.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	timeout := cfg.ScanTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	svc := &Service{
		store:        cfg.Store,
		app:          wrapManagedApp(cfg.App),
		agents:       cfg.AgentPool,
		hub:          NewHub(),
		sem:          make(chan struct{}, maxConcurrent),
		timeout:      timeout,
		auth:         NewAuth(cfg.AccessKey),
		cancels:      make(map[string]context.CancelFunc),
		scanNodeIDs:  make(map[string]string),
		taskSessions: make(map[string]string),
		taskNodeIDs:  make(map[string]string),
		taskCanceled: make(map[string]bool),
		sessionSeq:   make(map[string]uint64),
		endedTurns:   make(map[string]bool),
	}
	configAPI := managementapi.NewConfig(managementapi.ConfigOptions{
		Store: cfg.ConfigStore,
		Build: cfg.AppFactory,
		Apply: svc.swapApp,
		Broadcast: func(config *types.DistributeConfig) {
			if svc.agents != nil {
				svc.agents.BroadcastConfigReload(config)
			}
		},
	})
	svc.api = &managementapi.API{
		Sessions:  managementapi.NewSessions(cfg.Store, svc, generateID),
		Config:    configAPI,
		Scans:     managementapi.NewScans(svc, svc.hub),
		SCO:       managementapi.NewSCO(cfg.Store),
		Status:    svc,
		ServerURL: "/",
	}
	if cfg.AgentPool != nil {
		svc.api.Agents = cfg.AgentPool
		cfg.AgentPool.SetSessionLookup(svc)
	}
	return svc
}

func (s *Service) Hub() *Hub { return s.hub }

func (s *Service) SetAgentPool(pool *AgentPool) {
	s.agents = pool
	if s.api != nil {
		if pool == nil {
			s.api.Agents = nil
		} else {
			s.api.Agents = pool
		}
	}
	if pool == nil {
		return
	}
	pool.SetSessionLookup(s)
	pool.config = s.api.Config.Distribute
}

func (s *Service) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.cancels))
	for _, cancel := range s.cancels {
		cancels = append(cancels, cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	s.appMu.Lock()
	current := s.app
	s.app = nil
	app := retireManagedApp(current)
	s.appMu.Unlock()
	if app != nil {
		app.Close()
	}
}

func (s *Service) Status() *types.SystemStatus {
	app, release := s.acquireApp()
	status := &types.SystemStatus{
		Version:      config.Version,
		LlmAvailable: app != nil && app.Provider != nil,
	}
	if app != nil {
		status.LlmProvider = app.ProviderConfig.Provider
		status.LlmModel = app.ProviderConfig.Model
		status.LlmApiKeyConfigured = strings.TrimSpace(app.ProviderConfig.APIKey) != ""
	}
	release()
	if response, err := s.api.Config.GetConfig(context.Background(), &types.GetConfigRequest{}); err == nil {
		view := response.GetConfig()
		status.ConfigPath = view.GetPath()
		status.ConfigLoaded = view.GetLoaded()
		if active := view.GetLlm().GetActive(); active != nil {
			if status.LlmProvider == "" {
				status.LlmProvider = active.GetProvider()
			}
			if status.LlmModel == "" {
				status.LlmModel = active.GetModel()
			}
			status.LlmApiKeyConfigured = status.LlmApiKeyConfigured || active.GetApiKeyConfigured()
		}
	}
	return status
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

func (s *Service) aiAvailable() bool {
	app, release := s.acquireApp()
	defer release()
	return app != nil && app.Provider != nil
}

func wrapManagedApp(app *runner.App) *managedApp {
	if app == nil {
		return nil
	}
	return &managedApp{app: app}
}

func retireManagedApp(ref *managedApp) *runner.App {
	if ref == nil || ref.closed {
		return nil
	}
	ref.retired = true
	if ref.refs != 0 {
		return nil
	}
	ref.closed = true
	return ref.app
}

func (s *Service) acquireApp() (*runner.App, func()) {
	if s == nil {
		return nil, func() {}
	}
	s.appMu.Lock()
	ref := s.app
	if ref != nil && !ref.closed {
		ref.refs++
	}
	s.appMu.Unlock()
	if ref == nil || ref.closed {
		return nil, func() {}
	}

	var once sync.Once
	return ref.app, func() {
		once.Do(func() {
			var closeApp *runner.App
			s.appMu.Lock()
			ref.refs--
			if ref.refs == 0 && ref.retired && !ref.closed {
				ref.closed = true
				closeApp = ref.app
			}
			s.appMu.Unlock()
			if closeApp != nil {
				closeApp.Close()
			}
		})
	}
}

func (s *Service) swapApp(next *runner.App) {
	if s == nil || next == nil {
		return
	}
	s.appMu.Lock()
	prev := s.app
	if prev != nil && prev.app == next {
		s.appMu.Unlock()
		return
	}
	s.app = wrapManagedApp(next)
	closeApp := retireManagedApp(prev)
	s.appMu.Unlock()
	if closeApp != nil {
		closeApp.Close()
	}
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

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
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

// --- Chat session service methods ---

func (s *Service) TaskSession(taskID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sid, ok := s.taskSessions[taskID]
	return sid, ok
}

func (s *Service) registerSessionTask(taskID, sessionID, nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taskSessions[taskID] = sessionID
	if nodeID != "" {
		s.taskNodeIDs[taskID] = nodeID
	}
	delete(s.taskCanceled, taskID)
}

func (s *Service) finishSessionTask(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	canceled := s.taskCanceled[taskID]
	delete(s.taskSessions, taskID)
	delete(s.taskNodeIDs, taskID)
	delete(s.taskCanceled, taskID)
	return canceled
}

func (s *Service) CancelTurn(ctx context.Context, sessionID, turnID string) error {
	if _, err := s.store.GetSession(ctx, sessionID); err != nil {
		return err
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return ErrTurnNotFound
	}
	s.mu.Lock()
	sid, pending := s.taskSessions[turnID]
	nodeID := s.taskNodeIDs[turnID]
	if pending && sid == sessionID {
		s.taskCanceled[turnID] = true
	} else {
		pending = false
	}
	s.mu.Unlock()
	if !pending {
		return ErrTurnNotFound
	}
	if s.agents != nil && nodeID != "" {
		if err := s.agents.CancelTask(nodeID, turnID, sessionID); err != nil {
			return err
		}
	}
	s.BroadcastAOPEvent(sessionID, &aop.Event{
		SessionId: sessionID, TurnId: turnID, Emitter: "aiscan.web",
		Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "canceled"}},
	})
	return nil
}

func (s *Service) Upload(ctx context.Context, sessionID, filename string, data []byte) (*filepb.Result, error) {
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
		}
		return nil, fmt.Errorf("get upload session: %w", err)
	}
	if s.agents == nil {
		return nil, fmt.Errorf("no agent pool available")
	}
	nodeID := session.GetSession().GetNodeId()
	if nodeID == "" {
		return nil, fmt.Errorf("session has no assigned node")
	}

	taskID := generateID()
	resultCh, err := s.agents.dispatchMessage(nodeID, taskID, &filepb.ProtocolMessage{Message: &filepb.ProtocolMessage_UploadRequest{UploadRequest: &filepb.UploadRequest{
		SessionId: sessionID, Filename: filename, MediaType: http.DetectContentType(data), Data: data,
	}}})
	if err != nil {
		return nil, fmt.Errorf("agent dispatch failed: %w", err)
	}

	select {
	case res, ok := <-resultCh:
		if !ok {
			return nil, fmt.Errorf("agent disconnected during upload")
		}
		result := res.File
		if result == nil {
			return nil, fmt.Errorf("agent upload returned no result envelope")
		}
		s.broadcastSystemMessage(sessionID, SysFileUploaded,
			fmt.Sprintf("File uploaded: %s → %s", filename, result.Path),
			map[string]any{"filename": filename, "path": result.Path})
		return result, nil
	case <-ctx.Done():
		_ = s.agents.CancelTask(nodeID, taskID)
		return nil, ctx.Err()
	}
}

func (s *Service) DeleteSession(ctx context.Context, id string) error {
	s.closeRemoteSession(id)
	return s.store.DeleteSession(ctx, id)
}

func (s *Service) BroadcastAOPEvent(sessionID string, event *aop.Event) {
	if s == nil || s.hub == nil || sessionID == "" || event == nil || event.Payload == nil {
		return
	}
	if !s.prepareAOPEvent(sessionID, event) {
		return
	}
	var cursor int64
	if s.store != nil {
		storedCursor, _, err := s.store.AppendAOPEvent(context.Background(), sessionID, event)
		if err != nil {
			return
		}
		cursor = storedCursor
	}
	s.broadcastAOPEvent(sessionID, event, cursor)
}

// publishUserMessage records the operator input in the durable AOP timeline.
// Node delivery remains the caller's RunTurn/Command request; this function
// does not create a second transport path.
func (s *Service) publishUserMessage(sessionID, turnID string, message *aop.Message) {
	if message == nil || len(message.Content) == 0 {
		return
	}
	userMessage := proto.CloneOf(message)
	if userMessage.Id == "" {
		userMessage.Id = generateID()
	}
	userMessage.Role = "user"
	s.BroadcastAOPEvent(sessionID, &aop.Event{
		SessionId: sessionID,
		TurnId:    turnID,
		Emitter:   "aiscan.web",
		Payload:   &aop.Event_Message{Message: userMessage},
	})
}

func (s *Service) prepareAOPEvent(sessionID string, event *aop.Event) bool {
	if event.SessionId == "" {
		event.SessionId = sessionID
	}
	if event.Id == "" {
		event.Id = generateID()
	}
	if event.EmittedAt == nil {
		event.EmittedAt = timestamppb.Now()
	}
	sequenceKey := event.SessionId
	if event.Seq == 0 && s.store != nil {
		s.eventMu.Lock()
		_, initialized := s.sessionSeq[sequenceKey]
		s.eventMu.Unlock()
		if !initialized {
			if maximum, err := s.store.MaxAOPEventSeq(context.Background(), sequenceKey); err == nil {
				s.eventMu.Lock()
				if _, exists := s.sessionSeq[sequenceKey]; !exists {
					s.sessionSeq[sequenceKey] = maximum
				}
				s.eventMu.Unlock()
			}
		}
	}
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	if event.GetTurnEnded() != nil && event.TurnId != "" {
		terminalKey := sequenceKey + "\x00" + event.TurnId
		if s.endedTurns[terminalKey] {
			return false
		}
		s.endedTurns[terminalKey] = true
	}
	if event.Seq == 0 {
		s.sessionSeq[sequenceKey]++
		event.Seq = s.sessionSeq[sequenceKey]
	} else if event.Seq > s.sessionSeq[sequenceKey] {
		s.sessionSeq[sequenceKey] = event.Seq
	}
	return true
}

func (s *Service) resetTurnTerminal(sessionID, turnID string) {
	if sessionID == "" || turnID == "" {
		return
	}
	s.eventMu.Lock()
	delete(s.endedTurns, sessionID+"\x00"+turnID)
	s.eventMu.Unlock()
}

func (s *Service) broadcastAOPEvent(sessionID string, event *aop.Event, cursor int64) {
	deliveryCursor := ""
	if cursor > 0 {
		deliveryCursor = strconv.FormatInt(cursor, 10)
	}
	s.hub.BroadcastAOP(sessionID, &aop.EventDelivery{Cursor: deliveryCursor, Event: event}, isReliableAOPEvent(event))
}

// broadcastHubError emits a hub-originated failure as an AOP error event: the
// code names a translatable template (mirrored under `sys.*` in the frontend
// locales), message is the English fallback, and params feed i18n
// interpolation via the aiscan.web extension.
func (s *Service) broadcastHubError(sessionID, code, message string, params map[string]any) {
	event := &aop.Event{
		Id: generateID(), EmittedAt: timestamppb.Now(), SessionId: sessionID, Emitter: "aiscan.web",
		Payload: &aop.Event_Error{Error: &aop.ProtocolError{Code: code, Message: message}},
	}
	if len(params) > 0 {
		if values, err := structpb.NewStruct(params); err == nil {
			_ = types.SetWebMessage(event, &types.WebMessageMetadata{Params: values})
		}
	}
	s.BroadcastAOPEvent(sessionID, event)
}

func (s *Service) broadcastHubTurnEnded(sessionID, turnID, code, message string) {
	ended := &aop.TurnEnded{StopReason: "error", Error: &aop.ProtocolError{Code: code, Message: message}}
	s.BroadcastAOPEvent(sessionID, &aop.Event{
		SessionId: sessionID, TurnId: turnID, Emitter: "aiscan.web",
		Payload: &aop.Event_TurnEnded{TurnEnded: ended},
	})
}

func isReliableAOPEvent(event *aop.Event) bool {
	switch payload := event.Payload.(type) {
	case *aop.Event_SessionEnded, *aop.Event_Error, *aop.Event_ToolResult, *aop.Event_TurnEnded, *aop.Event_Message:
		return true
	case *aop.Event_Status:
		// Status entries that drive durable UI state (eval/compact banners,
		// budget warnings) must survive reconnect; the rest are evictable.
		switch payload.Status.State {
		case types.EvalStateEnd, types.CompactStateEnd, "token_budget_warning":
			return true
		}
	}
	return false
}

// runHubCommand executes a product-level slash command that needs hub state.
// name is the canonical catalog name without its leading slash. Agent-scope
// commands never reach here; they fall through to the agent bridge.
func (s *Service) runHubCommand(sessionID, name, args string) {
	switch name {
	case "agents":
		s.handleAgentsCommand(sessionID)
	case "help":
		s.handleHelpCommand(sessionID)
	}
}

// parseCommand splits a leading "/verb args..." into its lowercased verb
// and the trimmed remainder. ok is false when content does not begin with a
// non-empty "/verb".
func parseCommand(content string) (cmd, args string, ok bool) {
	if !strings.HasPrefix(content, "/") {
		return "", "", false
	}
	rest := strings.TrimSpace(content[1:])
	if rest == "" {
		return "", "", false
	}
	if i := strings.IndexAny(rest, " \t\r\n"); i >= 0 {
		return strings.ToLower(rest[:i]), strings.TrimSpace(rest[i:]), true
	}
	return strings.ToLower(rest), "", true
}

// handleHelpCommand renders the merged "/" command catalog (hub-scope plus the
// bound agent's reported agent-scope commands) as a system message. Broadcast
// with an empty code so the frontend shows this dynamic, already-localized text
// verbatim instead of translating it.
func (s *Service) handleHelpCommand(sessionID string) {
	var b strings.Builder
	b.WriteString("**Commands**\n")
	for _, c := range s.SessionMenu(sessionID) {
		syntax := c.Usage
		if syntax == "" {
			syntax = c.Name
		}
		if c.Description != "" {
			fmt.Fprintf(&b, "- `%s` — %s\n", syntax, c.Description)
		} else {
			fmt.Fprintf(&b, "- `%s`\n", syntax)
		}
	}
	b.WriteString("\n`!<command>` 直接在 agent 上执行 shell/伪命令;其他文本作为对话发送给 agent。")
	s.broadcastSystemMessage(sessionID, "", b.String(), nil)
}

// SessionMenu is the web "/" command catalog for a session: the hub-scope
// commands plus the bound agent's reported agent-scope commands (its skills
// included). It falls back to the static agent-scope menu when no agent is
// bound, so the menu is populated even before an agent connects. This is the
// single source both SessionService/ListCommands and /help render from.
func (s *Service) SessionMenu(sessionID string) []*types.CommandSpec {
	hubSpecs := []*types.CommandSpec{
		{Name: "/help", Description: "查看命令面板"},
		{Name: "/agents", Description: "列出已连接的 agent"},
	}
	var agentSpecs []*types.CommandSpec
	if agent := s.sessionAgent(sessionID); agent != nil {
		agentSpecs = agent.commandSpecs()
	}
	if len(agentSpecs) == 0 {
		// Fall back to the static agent-scope menu when no agent is bound.
		r := &tui.AgentConsole{}
		agentSpecs = tui.WebMenuSpecs(r.StaticCommands())
	}
	return append(hubSpecs, agentSpecs...)
}

func (s *Service) handleAgentsCommand(sessionID string) {
	if s.agents == nil || s.agents.Count() == 0 {
		s.broadcastSystemMessage(sessionID, SysNoAgentsConnected, "No agents connected.", nil)
		return
	}
	agents := s.agents.List()
	list := make([]*types.AgentListEntry, 0, len(agents))
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d agent(s) connected:\n", len(agents)))
	for _, agentView := range agents {
		hello := agentView.GetHello()
		statusView := agentView.GetStatus()
		status := "idle"
		if agentView.GetBusy() {
			status = "busy"
		}
		shortID := hello.GetNodeId()
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		sb.WriteString(fmt.Sprintf("- **%s** (%s) — %s", hello.GetName(), shortID, status))
		entry := &types.AgentListEntry{Name: hello.GetName(), NodeId: shortID, Busy: agentView.GetBusy()}
		if statusView.GetModel() != "" {
			sb.WriteString(fmt.Sprintf(" — %s/%s", statusView.GetProvider(), statusView.GetModel()))
			entry.Provider = statusView.GetProvider()
			entry.Model = statusView.GetModel()
		}
		sb.WriteString("\n")
		list = append(list, entry)
	}
	s.broadcastSystemMessageMetadata(sessionID, sb.String(), &types.WebMessageMetadata{
		Code:      SysAgentsList,
		AgentList: &types.AgentListMetadata{Agents: list},
	})
}

func (s *Service) sessionAgent(sessionID string) *remoteAgent {
	session, err := s.store.GetSession(context.Background(), sessionID)
	if err != nil || session.GetSession().GetNodeId() == "" {
		return nil
	}
	if s.agents == nil {
		return nil
	}
	return s.agents.get(session.GetSession().GetNodeId())
}

func (s *Service) handleAgentRun(sessionID string, request *aop.RunTurnRequest) {
	agent := s.sessionAgent(sessionID)
	if agent == nil {
		s.broadcastSystemMessage(sessionID, SysAgentNotConnected,
			"Agent is not connected. Reconnect the agent to continue chatting.", nil)
		return
	}

	taskID := strings.TrimSpace(request.TurnId)
	if taskID == "" {
		taskID = generateID()
	}
	request.TurnId = taskID
	request.SessionId = sessionID
	s.resetTurnTerminal(sessionID, taskID)
	s.registerSessionTask(taskID, sessionID, agent.NodeID())
	resultCh, err := s.agents.DispatchRun(agent.NodeID(), request)
	if err != nil {
		s.finishSessionTask(taskID)
		s.broadcastHubTurnEnded(sessionID, taskID, "dispatch_failed", err.Error())
		return
	}

	go func() {
		res, ok := <-resultCh
		canceled := s.finishSessionTask(taskID)
		if canceled {
			return
		}
		if !ok {
			s.broadcastHubTurnEnded(sessionID, taskID, "agent_disconnected", "agent disconnected")
			return
		}
		if res.Err != "" {
			s.broadcastHubTurnEnded(sessionID, taskID, "agent_run_failed", res.Err)
		}
	}()
}

func (s *Service) ExecuteSessionCommand(sessionID, line string) (string, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("command line is required")
	}
	if _, err := s.store.GetSession(context.Background(), sessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
		}
		return "", err
	}
	s.publishUserMessage(sessionID, "", &aop.Message{Role: "user", Content: []*aop.Content{aop.Text(line)}})
	if verb, args, ok := parseCommand(line); ok {
		switch verb {
		case "help", "agents":
			operationID := generateID()
			go s.runHubCommand(sessionID, verb, args)
			return operationID, nil
		case "clear":
			return "", fmt.Errorf("clear requires ResetSession")
		case "stop":
			return "", fmt.Errorf("stop requires CancelTurn")
		case "exit", "quit":
			return "", fmt.Errorf("exit requires CloseSession")
		case "continue", "followup":
			return "", fmt.Errorf("%s requires RunTurn", verb)
		case "scan":
			return "", fmt.Errorf("scan is not available through the chat protocol")
		}
	}
	agent := s.sessionAgent(sessionID)
	if agent == nil {
		return "", fmt.Errorf("agent is not connected")
	}
	taskID := generateID()
	s.registerSessionTask(taskID, sessionID, agent.NodeID())
	resultCh, err := s.agents.DispatchCommand(agent.NodeID(), taskID, &types.CommandRequest{SessionId: sessionID, Line: line})
	if err != nil {
		s.finishSessionTask(taskID)
		return "", err
	}
	go func() {
		res, ok := <-resultCh
		canceled := s.finishSessionTask(taskID)
		if !ok || canceled {
			return
		}
		if res.Err != "" {
			s.broadcastHubError(sessionID, "", res.Err, nil)
		}
	}()
	return taskID, nil
}

func (s *Service) closeRemoteSession(sessionID string) {
	session, err := s.store.GetSession(context.Background(), sessionID)
	if err != nil || s.agents == nil || session.GetSession().GetNodeId() == "" {
		return
	}
	requestID := "close:" + sessionID
	_ = s.agents.sendAgentMessage(session.GetSession().GetNodeId(), requestID, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CloseSessionRequest{CloseSessionRequest: &aop.CloseSessionRequest{
		SessionId: sessionID, Reason: "completed",
	}}})
}

// broadcastSystemMessage persists + broadcasts a system message. code names a
// translatable template rendered client-side via i18n (see the Sys* codes);
// fallback is the English text kept in Content for non-i18n consumers, logs and
// tests. params feeds i18n interpolation and is stored next to code so the
// message stays localizable after a reload.
func (s *Service) broadcastSystemMessage(sessionID, code, fallback string, params map[string]any) {
	metadata := &types.WebMessageMetadata{Code: code}
	if code != "" {
		metadata.Params, _ = structpb.NewStruct(params)
	}
	s.broadcastSystemMessageMetadata(sessionID, fallback, metadata)
}

func (s *Service) broadcastSystemMessageMetadata(sessionID, fallback string, metadata *types.WebMessageMetadata) {
	event := &aop.Event{
		Id: generateID(), EmittedAt: timestamppb.Now(), SessionId: sessionID, Emitter: "aiscan.web",
		Payload: &aop.Event_Message{Message: &aop.Message{
			Id: generateID(), Role: "system", Content: []*aop.Content{aop.Text(fallback)},
		}},
	}
	if metadata != nil && (metadata.GetCode() != "" || metadata.GetNodeId() != "" || metadata.GetParams() != nil || metadata.GetAgentList() != nil) {
		_ = types.SetWebMessage(event, metadata)
	}
	s.BroadcastAOPEvent(sessionID, event)
}

func (s *Service) broadcastScanComplete(scanID string) {
	s.mu.Lock()
	sid, ok := s.taskSessions[scanID]
	s.mu.Unlock()
	if !ok {
		return
	}
	if s.finishSessionTask(scanID) {
		return
	}
	_ = s.store.LinkScanToSession(context.Background(), sid, scanID)
	value, err := anypb.New(&types.SessionScanEvent{ScanId: scanID, Status: types.ScanStatus_SCAN_STATUS_COMPLETED})
	if err != nil {
		return
	}
	s.BroadcastAOPEvent(sid, &aop.Event{
		SessionId: sid,
		Emitter:   "aiscan.web",
		Payload:   &aop.Event_Extension{Extension: value},
	})
}
