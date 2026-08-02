package web

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
	chatpb "github.com/chainreactors/aiscan/pkg/types/chat"
	commandpb "github.com/chainreactors/aiscan/pkg/types/command"
	configpb "github.com/chainreactors/aiscan/pkg/types/config"
	ext "github.com/chainreactors/aiscan/pkg/types/extensions"
	scanpb "github.com/chainreactors/aiscan/pkg/types/scan"
	systempb "github.com/chainreactors/aiscan/pkg/types/system"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ConfigStore interface {
	GetDistributeConfig(ctx context.Context) (path string, loaded bool, cfg *configpb.DistributeConfig, err error)
	PrepareDistributeConfig(ctx context.Context, cfg *configpb.DistributeConfig) (*PreparedConfig, error)
	CommitDistributeConfig(ctx context.Context, prepared *PreparedConfig) error
	DiscardDistributeConfig(prepared *PreparedConfig)
}

type PreparedConfig struct {
	Config      *configpb.DistributeConfig
	RuntimePath string
	TargetPath  string
}

type ServiceConfig struct {
	Store         *SQLiteStore
	App           *runner.App
	ConfigStore   ConfigStore
	AppFactory    func(ctx context.Context, prepared *PreparedConfig) (*runner.App, error)
	AgentPool     *AgentPool
	MaxConcurrent int
	ScanTimeout   time.Duration
}

type Service struct {
	store   *SQLiteStore
	appMu   sync.Mutex
	app     *managedApp
	saveMu  sync.Mutex
	config  ConfigStore
	reload  func(ctx context.Context, prepared *PreparedConfig) (*runner.App, error)
	agents  *AgentPool
	hub     *Hub
	sem     chan struct{}
	timeout time.Duration

	mu           sync.Mutex
	cancels      map[string]context.CancelFunc
	scanAgents   map[string]string
	taskSessions map[string]string // taskID → sessionID
	taskAgents   map[string]string // taskID → agentID
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
		config:       cfg.ConfigStore,
		reload:       cfg.AppFactory,
		agents:       cfg.AgentPool,
		hub:          NewHub(),
		sem:          make(chan struct{}, maxConcurrent),
		timeout:      timeout,
		cancels:      make(map[string]context.CancelFunc),
		scanAgents:   make(map[string]string),
		taskSessions: make(map[string]string),
		taskAgents:   make(map[string]string),
		taskCanceled: make(map[string]bool),
		sessionSeq:   make(map[string]uint64),
		endedTurns:   make(map[string]bool),
	}
	if cfg.AgentPool != nil {
		cfg.AgentPool.SetSessionLookup(svc)
	}
	return svc
}

func (s *Service) Hub() *Hub { return s.hub }

func (s *Service) SetAgentPool(pool *AgentPool) {
	s.agents = pool
	pool.SetSessionLookup(s)
	pool.config = s.GetDistributeConfig
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

func (s *Service) Status() *systempb.Status {
	app, release := s.acquireApp()
	status := &systempb.Status{
		Version:      config.Version,
		LlmAvailable: app != nil && app.Provider != nil,
	}
	if app != nil {
		status.LlmProvider = app.ProviderConfig.Provider
		status.LlmModel = app.ProviderConfig.Model
		status.LlmApiKeyConfigured = strings.TrimSpace(app.ProviderConfig.APIKey) != ""
	}
	release()
	if s.config != nil {
		if path, loaded, dc, err := s.config.GetDistributeConfig(context.Background()); err == nil {
			status.ConfigPath = path
			status.ConfigLoaded = loaded
			if active := config.ActiveLLMProvider(dc.GetLlm()); active != nil {
				if status.LlmProvider == "" {
					status.LlmProvider = active.Provider
				}
				if status.LlmModel == "" {
					status.LlmModel = active.Model
				}
				status.LlmApiKeyConfigured = status.LlmApiKeyConfigured || active.ApiKey != ""
			}
		}
	}
	return status
}

func (s *Service) GetConfigView(ctx context.Context) (*configpb.ConfigView, error) {
	if s.config == nil {
		return nil, fmt.Errorf("config store is not configured")
	}
	path, loaded, dc, err := s.config.GetDistributeConfig(ctx)
	if err != nil {
		return nil, err
	}
	return ConfigViewFromDistribute(dc, path, loaded), nil
}

func (s *Service) SaveConfig(ctx context.Context, cfg *configpb.DistributeConfig) (*configpb.ConfigView, error) {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if s.config == nil {
		return nil, fmt.Errorf("config store is not configured")
	}
	if err := ValidateLLMConfig(cfg.GetLlm()); err != nil {
		return nil, err
	}
	prepared, err := s.config.PrepareDistributeConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			s.config.DiscardDistributeConfig(prepared)
		}
	}()
	if prepared == nil {
		return nil, fmt.Errorf("config store returned no prepared config")
	}
	if err := ValidateLLMConfig(prepared.Config.GetLlm()); err != nil {
		return nil, err
	}

	var nextApp *runner.App
	if s.reload != nil {
		nextApp, err = s.reload(ctx, prepared)
		if err != nil {
			view, _ := s.GetConfigView(ctx)
			return view, fmt.Errorf("reload aiscan runtime: %w", err)
		}
		if nextApp == nil {
			return nil, fmt.Errorf("reload aiscan runtime returned no app")
		}
	}
	if err := s.config.CommitDistributeConfig(ctx, prepared); err != nil {
		if nextApp != nil {
			nextApp.Close()
		}
		return nil, err
	}
	committed = true
	if nextApp != nil {
		s.swapApp(nextApp)
	}
	// Tell connected agents to hot-swap their own provider too — the hub reload
	// above only refreshes the hub's in-process runtime, not the agent subprocesses.
	if s.agents != nil {
		s.agents.BroadcastConfigReload(prepared.Config)
	}
	return s.GetConfigView(ctx)
}

func (s *Service) ActivateLLMProfile(ctx context.Context, id string) (*configpb.ConfigView, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("LLM profile id is required")
	}
	if s.config == nil {
		return nil, fmt.Errorf("config store is not configured")
	}
	_, _, stored, err := s.config.GetDistributeConfig(ctx)
	if err != nil {
		return nil, err
	}
	found := false
	for _, profile := range stored.GetLlm().GetProviders() {
		if profile.Id == id {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("LLM profile %q was not found", id)
	}
	cfg := proto.Clone(stored).(*configpb.DistributeConfig)
	if cfg.Llm == nil {
		cfg.Llm = &configpb.LLMConfig{}
	}
	cfg.Llm.ActiveProfile = id
	return s.SaveConfig(ctx, cfg)
}

func (s *Service) GetDistributeConfig(ctx context.Context) (*configpb.DistributeConfig, error) {
	if s.config == nil {
		return nil, fmt.Errorf("config store is not configured")
	}
	_, _, dc, err := s.config.GetDistributeConfig(ctx)
	return dc, err
}

func (s *Service) SubmitScan(ctx context.Context, target, mode string, verify, sniper, deep bool) (*scanpb.Scan, error) {
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
	scan := &scanpb.Scan{
		Id:        generateID(),
		Target:    target,
		Mode:      mode,
		Options:   &scanpb.ScanOptions{Verify: verify, Sniper: sniper, Deep: deep},
		Status:    scanpb.ScanStatus_SCAN_STATUS_QUEUED,
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

func (s *Service) GetScan(ctx context.Context, id string) (*scanpb.Scan, error) {
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
	agentID := s.scanAgents[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.hub.BroadcastScan(scanFailedEvent(id, "scan canceled", true), true)
	if agentID != "" && s.agents != nil {
		_ = s.agents.CancelTask(agentID, id)
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
		delete(s.scanAgents, scanID)
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
	scan.Status = scanpb.ScanStatus_SCAN_STATUS_RUNNING
	scan.UpdatedAt = nowProto()
	changed, err := s.store.TransitionScan(context.Background(), scan, scanpb.ScanStatus_SCAN_STATUS_QUEUED)
	if err != nil || !changed {
		return
	}

	s.hub.BroadcastScan(scanStatusEvent(scanID, scanpb.ScanStatus_SCAN_STATUS_RUNNING), false)

	// Try agent dispatch first, fall back to local execution.
	if s.agents != nil && s.agents.Count() > 0 {
		s.runScanViaAgent(ctx, scan)
		return
	}
	s.runScanLocally(ctx, scan)
}

func (s *Service) runScanViaAgent(ctx context.Context, scan *scanpb.Scan) {
	agent := s.agents.Pick()
	if agent == nil {
		_, _ = s.failScan(scan, "no agents available")
		return
	}
	s.mu.Lock()
	s.scanAgents[scan.Id] = agent.id
	s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		s.finishScanContext(scan, err)
		return
	}

	cmd := "scan " + strings.Join(scanArgsForScan(scan), " ")
	args, _ := aop.JSONValue(map[string]any{"command": cmd})
	resultCh, err := s.agents.DispatchToolCall(agent.id, scan.Id, &aop.ToolCall{
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
		_ = s.agents.CancelTask(agent.id, scan.Id)
		s.finishScanContext(scan, ctx.Err())
		return
	case res, ok = <-resultCh:
	}
	if ctx.Err() != nil {
		_ = s.agents.CancelTask(agent.id, scan.Id)
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

func (s *Service) runScanLocally(ctx context.Context, scan *scanpb.Scan) {
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

func (s *Service) finishScanContext(scan *scanpb.Scan, err error) {
	if err == nil {
		return
	}
	if err == context.DeadlineExceeded {
		_, _ = s.failScan(scan, "scan timed out")
		return
	}
	next := proto.Clone(scan).(*scanpb.Scan)
	next.Status = scanpb.ScanStatus_SCAN_STATUS_CANCELED
	next.UpdatedAt = nowProto()
	_, _ = s.store.TransitionScan(context.Background(), next, scanpb.ScanStatus_SCAN_STATUS_QUEUED, scanpb.ScanStatus_SCAN_STATUS_RUNNING)
}

func (s *Service) completeScan(ctx context.Context, scan *scanpb.Scan) (bool, error) {
	nodes, err := s.store.ListSCONodesByScanID(ctx, scan.Id, "", 100000)
	if err != nil {
		return false, fmt.Errorf("load scan SCO facts: %w", err)
	}
	next := proto.Clone(scan).(*scanpb.Scan)
	next.Status = scanpb.ScanStatus_SCAN_STATUS_COMPLETED
	next.Report = buildMarkdownReport(scan.Target, scan.Mode, nodes, defaultReportLang)
	next.Error = ""
	next.UpdatedAt = nowProto()
	changed, err := s.store.TransitionScan(ctx, next, scanpb.ScanStatus_SCAN_STATUS_RUNNING)
	if err != nil || !changed {
		return changed, err
	}
	proto.Merge(scan, next)
	s.hub.BroadcastScan(scanCompletedEvent(scan.Id), true)
	s.broadcastScanComplete(scan.Id)
	return true, nil
}

func (s *Service) failScan(scan *scanpb.Scan, errMsg string) (bool, error) {
	next := proto.Clone(scan).(*scanpb.Scan)
	next.Status = scanpb.ScanStatus_SCAN_STATUS_FAILED
	next.Error = errMsg
	next.UpdatedAt = nowProto()
	changed, err := s.store.TransitionScan(context.Background(), next, scanpb.ScanStatus_SCAN_STATUS_QUEUED, scanpb.ScanStatus_SCAN_STATUS_RUNNING)
	if err != nil || !changed {
		return changed, err
	}
	proto.Merge(scan, next)
	s.hub.BroadcastScan(scanFailedEvent(scan.Id, errMsg, false), true)
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

func scanArgsForScan(scan *scanpb.Scan) []string {
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
	scan   *scanpb.Scan
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
		if current.Status == scanpb.ScanStatus_SCAN_STATUS_CANCELED {
			return 0, context.Canceled
		}
		current.Progress = line
		current.UpdatedAt = nowProto()
		changed, err := w.store.TransitionScan(context.Background(), current, scanpb.ScanStatus_SCAN_STATUS_RUNNING)
		if err != nil {
			return 0, err
		}
		if !changed {
			return 0, context.Canceled
		}
		w.scan = current

		w.hub.BroadcastScan(scanProgressEvent(w.scanID, line), false)
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

func (s *Service) registerSessionTask(taskID, sessionID, agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taskSessions[taskID] = sessionID
	if agentID != "" {
		s.taskAgents[taskID] = agentID
	}
	delete(s.taskCanceled, taskID)
}

func (s *Service) finishSessionTask(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	canceled := s.taskCanceled[taskID]
	delete(s.taskSessions, taskID)
	delete(s.taskAgents, taskID)
	delete(s.taskCanceled, taskID)
	return canceled
}

func (s *Service) CancelSession(ctx context.Context, sessionID string) error {
	if _, err := s.store.GetSession(ctx, sessionID); err != nil {
		return err
	}

	type activeTask struct {
		taskID  string
		agentID string
	}
	var tasks []activeTask
	s.mu.Lock()
	for taskID, sid := range s.taskSessions {
		if sid != sessionID {
			continue
		}
		tasks = append(tasks, activeTask{taskID: taskID, agentID: s.taskAgents[taskID]})
		s.taskCanceled[taskID] = true
	}
	s.mu.Unlock()

	if len(tasks) == 0 {
		s.broadcastSystemMessage(sessionID, SysNoRunningTask, "No running task.", nil)
		return nil
	}
	if s.agents != nil {
		for _, task := range tasks {
			if task.agentID != "" {
				_ = s.agents.CancelTask(task.agentID, task.taskID, sessionID)
			}
		}
	}
	s.broadcastSystemMessage(sessionID, SysPaused, "Paused.", nil)
	return nil
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
	agentID := s.taskAgents[turnID]
	if pending && sid == sessionID {
		s.taskCanceled[turnID] = true
	} else {
		pending = false
	}
	s.mu.Unlock()
	if !pending {
		return ErrTurnNotFound
	}
	if s.agents != nil && agentID != "" {
		if err := s.agents.CancelTask(agentID, turnID, sessionID); err != nil {
			return err
		}
	}
	s.BroadcastAOPEvent(sessionID, &aop.Event{
		SessionId: sessionID, TurnId: turnID, Emitter: "aiscan.web",
		Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "canceled"}},
	})
	return nil
}

func (s *Service) HandleFileUpload(ctx context.Context, sessionID, filename string, data []byte) (*filepb.Result, error) {
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
	agentID := session.GetSession().GetParticipant()
	if agentID == "" {
		return nil, fmt.Errorf("session has no assigned agent")
	}

	taskID := generateID()
	resultCh, err := s.agents.dispatchMessage(agentID, taskID, &filepb.ProtocolMessage{Message: &filepb.ProtocolMessage_UploadRequest{UploadRequest: &filepb.UploadRequest{
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
		_ = s.agents.CancelTask(agentID, taskID)
		return nil, ctx.Err()
	}
}

func (s *Service) CreateSession(ctx context.Context, agentID, title string) (*chatpb.SessionRecord, error) {
	var agentName string
	if s.agents != nil {
		if info := s.agents.get(agentID); info != nil {
			agentName = info.name
		}
	}
	now := nowProto()
	session := &chatpb.SessionRecord{
		Session:   &aop.Session{Id: generateID(), State: SessionStateOpen, Participant: agentID, Title: title},
		AgentName: agentName,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.CreateSession(ctx, session); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return session, nil
}

func (s *Service) GetSession(ctx context.Context, id string) (*chatpb.SessionRecord, error) {
	return s.store.GetSession(ctx, id)
}

func (s *Service) ListSessions(ctx context.Context) ([]*chatpb.SessionRecord, error) {
	return s.store.ListSessions(ctx, 100)
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

func (s *Service) broadcastUserMessage(sessionID, turnID string, message *aop.Message) {
	if message == nil || len(message.Content) == 0 {
		return
	}
	canonical := proto.Clone(message).(*aop.Message)
	if canonical.Id == "" {
		canonical.Id = generateID()
	}
	canonical.Role = "user"
	s.BroadcastAOPEvent(sessionID, &aop.Event{
		SessionId: sessionID,
		TurnId:    turnID,
		Emitter:   "aiscan.web",
		Payload:   &aop.Event_Message{Message: canonical},
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
	s.hub.BroadcastAOP(sessionID, AOPDelivery{Cursor: cursor, Event: event}, isReliableAOPEvent(event))
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
			_ = ext.SetWebMessage(event, ext.WebMessageExtension{Params: values})
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
		case ext.EvalStateEnd, ext.CompactStateEnd, "token_budget_warning":
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
func (s *Service) SessionMenu(sessionID string) []*commandpb.Spec {
	hubSpecs := []*commandpb.Spec{
		{Name: "/help", Description: "查看命令面板"},
		{Name: "/agents", Description: "列出已连接的 agent"},
	}
	agentSpecs := s.sessionAgent(sessionID).commandSpecs()
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
	list := make([]map[string]any, 0, len(agents))
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d agent(s) connected:\n", len(agents)))
	for _, agentView := range agents {
		hello := agentView.GetHello()
		statusView := agentView.GetStatus()
		status := "idle"
		if agentView.GetBusy() {
			status = "busy"
		}
		shortID := hello.GetAgentId()
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		sb.WriteString(fmt.Sprintf("- **%s** (%s) — %s", hello.GetName(), shortID, status))
		entry := map[string]any{"name": hello.GetName(), "id": shortID, "busy": agentView.GetBusy()}
		if statusView.GetModel() != "" {
			sb.WriteString(fmt.Sprintf(" — %s/%s", statusView.GetProvider(), statusView.GetModel()))
			entry["provider"] = statusView.GetProvider()
			entry["model"] = statusView.GetModel()
		}
		sb.WriteString("\n")
		list = append(list, entry)
	}
	s.broadcastSystemMessage(sessionID, SysAgentsList, sb.String(),
		map[string]any{"count": len(agents), "agents": list})
}

func (s *Service) sessionAgent(sessionID string) *remoteAgent {
	session, err := s.store.GetSession(context.Background(), sessionID)
	if err != nil || session.GetSession().GetParticipant() == "" {
		return nil
	}
	if s.agents == nil {
		return nil
	}
	return s.agents.get(session.GetSession().GetParticipant())
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
	s.registerSessionTask(taskID, sessionID, agent.id)

	resultCh, err := s.agents.DispatchRun(agent.id, request)
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

func (s *Service) handleAgentCommand(sessionID, line string) {
	if _, err := s.ExecuteSessionCommand(sessionID, line); err != nil {
		s.broadcastHubError(sessionID, "", err.Error(), nil)
	}
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
	s.broadcastUserMessage(sessionID, "", &aop.Message{Role: "user", Content: []*aop.Content{aop.Text(line)}})
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
	s.registerSessionTask(taskID, sessionID, agent.id)
	resultCh, err := s.agents.DispatchCommand(agent.id, taskID, &commandpb.Request{SessionId: sessionID, Line: line})
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
	if err != nil || s.agents == nil || session.GetSession().GetParticipant() == "" {
		return
	}
	requestID := "close:" + sessionID
	_ = s.agents.sendAgentMessage(session.GetSession().GetParticipant(), requestID, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CloseSessionRequest{CloseSessionRequest: &aop.CloseSessionRequest{
		SessionId: sessionID, Reason: "completed",
	}}})
}

// broadcastSystemMessage persists + broadcasts a system message. code names a
// translatable template rendered client-side via i18n (see the Sys* codes);
// fallback is the English text kept in Content for non-i18n consumers, logs and
// tests. params feeds i18n interpolation and is stored next to code so the
// message stays localizable after a reload.
func (s *Service) broadcastSystemMessage(sessionID, code, fallback string, params map[string]any) {
	event := &aop.Event{
		Id: generateID(), EmittedAt: timestamppb.Now(), SessionId: sessionID, Emitter: "aiscan.web",
		Payload: &aop.Event_Message{Message: &aop.Message{
			Id: generateID(), Role: "system", Content: []*aop.Content{aop.Text(fallback)},
		}},
	}
	if code != "" {
		encodedParams, _ := structpb.NewStruct(params)
		_ = ext.SetWebMessage(event, ext.WebMessageExtension{Code: code, Params: encodedParams})
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
	value, err := anypb.New(&scanpb.SessionScanEvent{ScanId: scanID, Status: scanpb.ScanStatus_SCAN_STATUS_COMPLETED})
	if err != nil {
		return
	}
	s.BroadcastAOPEvent(sid, &aop.Event{
		SessionId: sid,
		Emitter:   "aiscan.web",
		Payload:   &aop.Event_Extension{Extension: value},
	})
}
