package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	ext "github.com/chainreactors/aiscan/aop/aiscan/extensions"
	scanpb "github.com/chainreactors/aiscan/aop/aiscan/scan"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/runner"
	"github.com/chainreactors/aiscan/pkg/tui"
	scantool "github.com/chainreactors/aiscan/tools/scan"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ConfigStore interface {
	GetDistributeConfig(ctx context.Context) (path string, loaded bool, cfg config.DistributeConfig, err error)
	PrepareDistributeConfig(ctx context.Context, cfg config.DistributeConfig) (*PreparedConfig, error)
	CommitDistributeConfig(ctx context.Context, prepared *PreparedConfig) error
	DiscardDistributeConfig(prepared *PreparedConfig)
}

type PreparedConfig struct {
	Config      config.DistributeConfig
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

func (s *Service) Status() ServiceStatus {
	app, release := s.acquireApp()
	status := ServiceStatus{
		Version:      config.Version,
		LLMAvailable: app != nil && app.Provider != nil,
	}
	if app != nil {
		status.LLMProvider = app.ProviderConfig.Provider
		status.LLMModel = app.ProviderConfig.Model
		status.LLMAPIKeyConfigured = strings.TrimSpace(app.ProviderConfig.APIKey) != ""
	}
	release()
	if s.config != nil {
		if path, loaded, dc, err := s.config.GetDistributeConfig(context.Background()); err == nil {
			status.ConfigPath = path
			status.ConfigLoaded = loaded
			active := dc.LLM.Active()
			if status.LLMProvider == "" {
				status.LLMProvider = active.Provider
			}
			if status.LLMModel == "" {
				status.LLMModel = active.Model
			}
			status.LLMAPIKeyConfigured = status.LLMAPIKeyConfigured || active.APIKey != ""
		}
	}
	return status
}

func (s *Service) GetConfigStatus(ctx context.Context) (ConfigStatus, error) {
	if s.config == nil {
		return ConfigStatus{}, fmt.Errorf("config store is not configured")
	}
	path, loaded, dc, err := s.config.GetDistributeConfig(ctx)
	if err != nil {
		return ConfigStatus{}, err
	}
	return ConfigStatusFromDistribute(&dc, path, loaded), nil
}

func (s *Service) SaveConfig(ctx context.Context, cfg config.DistributeConfig) (ConfigStatus, error) {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if s.config == nil {
		return ConfigStatus{}, fmt.Errorf("config store is not configured")
	}
	if err := ValidateLLMConfig(cfg.LLM); err != nil {
		return ConfigStatus{}, err
	}
	prepared, err := s.config.PrepareDistributeConfig(ctx, cfg)
	if err != nil {
		return ConfigStatus{}, err
	}
	committed := false
	defer func() {
		if !committed {
			s.config.DiscardDistributeConfig(prepared)
		}
	}()
	if prepared == nil {
		return ConfigStatus{}, fmt.Errorf("config store returned no prepared config")
	}
	if err := ValidateLLMConfig(prepared.Config.LLM); err != nil {
		return ConfigStatus{}, err
	}

	var nextApp *runner.App
	if s.reload != nil {
		nextApp, err = s.reload(ctx, prepared)
		if err != nil {
			cs, _ := s.GetConfigStatus(ctx)
			return cs, fmt.Errorf("reload aiscan runtime: %w", err)
		}
		if nextApp == nil {
			return ConfigStatus{}, fmt.Errorf("reload aiscan runtime returned no app")
		}
	}
	if err := s.config.CommitDistributeConfig(ctx, prepared); err != nil {
		if nextApp != nil {
			nextApp.Close()
		}
		return ConfigStatus{}, err
	}
	committed = true
	if nextApp != nil {
		s.swapApp(nextApp)
	}
	// Tell connected agents to hot-swap their own provider too — the hub reload
	// above only refreshes the hub's in-process runtime, not the agent subprocesses.
	if s.agents != nil {
		s.agents.BroadcastConfigReload()
	}
	return s.GetConfigStatus(ctx)
}

func (s *Service) ActivateLLMProfile(ctx context.Context, id string) (ConfigStatus, error) {
	if strings.TrimSpace(id) == "" {
		return ConfigStatus{}, fmt.Errorf("LLM profile id is required")
	}
	if s.config == nil {
		return ConfigStatus{}, fmt.Errorf("config store is not configured")
	}
	_, _, cfg, err := s.config.GetDistributeConfig(ctx)
	if err != nil {
		return ConfigStatus{}, err
	}
	found := false
	for _, profile := range cfg.LLM.Providers {
		if profile.ID == id {
			found = true
			break
		}
	}
	if !found {
		return ConfigStatus{}, fmt.Errorf("LLM profile %q was not found", id)
	}
	cfg.LLM.ActiveProfile = id
	return s.SaveConfig(ctx, cfg)
}

func (s *Service) GetDistributeConfig(ctx context.Context) (config.DistributeConfig, error) {
	if s.config == nil {
		return config.DistributeConfig{}, fmt.Errorf("config store is not configured")
	}
	_, _, dc, err := s.config.GetDistributeConfig(ctx)
	return dc, err
}

func (s *Service) SubmitScan(ctx context.Context, target, mode string, verify, sniper, deep bool) (*ScanJob, error) {
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

	now := time.Now()
	job := &ScanJob{
		ID:        generateID(),
		Target:    target,
		Mode:      mode,
		Verify:    verify,
		Sniper:    sniper,
		Deep:      deep,
		Status:    StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.store.Create(ctx, job); err != nil {
		return nil, fmt.Errorf("store create: %w", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[job.ID] = cancel
	s.mu.Unlock()
	go func() { //nolint:gosec // G118: background scan intentionally outlives the request
		defer cancel()
		s.runScan(runCtx, job.ID)
	}()

	return job, nil
}

func (s *Service) GetScan(ctx context.Context, id string) (*ScanJob, error) {
	job, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	refreshStructuredAssets(job)
	return job, nil
}

func (s *Service) ListScans(ctx context.Context) ([]*ScanJob, error) {
	jobs, err := s.store.List(ctx, 100)
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		refreshStructuredAssets(job)
	}
	return jobs, nil
}

func refreshStructuredAssets(job *ScanJob) {
	if job != nil && job.Result != nil && (len(job.Result.Services) > 0 || len(job.Result.WebProbes) > 0) {
		job.Result.Assets = scantool.AggregateStructuredResult(job.Result)
	}
}

func (s *Service) CancelScan(id string) error {
	ctx := context.Background()
	job, err := s.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrScanNotFound, id)
		}
		return err
	}
	if job.Status == StatusCanceled {
		return nil
	}
	if job.Status != StatusRunning && job.Status != StatusQueued {
		return fmt.Errorf("%w: scan %s is %s", ErrScanNotCancelable, id, job.Status)
	}
	job.Status = StatusCanceled
	job.UpdatedAt = time.Now()
	changed, err := s.store.TransitionScan(ctx, job, StatusRunning, StatusQueued)
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
		if current.Status == StatusCanceled {
			return nil
		}
		return fmt.Errorf("%w: scan %s is %s", ErrScanNotCancelable, id, current.Status)
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

// GetReport re-renders the report in the requested language from the stored
// structured result, so a zh user gets a zh report even though the scan ran
// once. It falls back to the report frozen at scan time when the structured
// result is no longer around.
func (s *Service) GetReport(ctx context.Context, id, lang string) (string, error) {
	job, err := s.GetScan(ctx, id)
	if err != nil {
		return "", err
	}
	if job.Result != nil {
		return buildMarkdownReport(job.Target, job.Mode, job.Result, lang), nil
	}
	return job.Report, nil
}

func (s *Service) runScan(runCtx context.Context, jobID string) {
	defer func() {
		s.mu.Lock()
		delete(s.cancels, jobID)
		delete(s.scanAgents, jobID)
		s.mu.Unlock()
	}()
	defer func() {
		if recovered := recover(); recovered != nil {
			if job, err := s.store.Get(context.Background(), jobID); err == nil {
				_, _ = s.failJob(job, fmt.Sprintf("scan runtime panic: %v", recovered))
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

	job, err := s.store.Get(ctx, jobID)
	if err != nil {
		return
	}
	job.Status = StatusRunning
	job.UpdatedAt = time.Now()
	changed, err := s.store.TransitionScan(context.Background(), job, StatusQueued)
	if err != nil || !changed {
		return
	}

	s.hub.BroadcastScan(scanStatusEvent(jobID, StatusRunning), false)

	// Try agent dispatch first, fall back to local execution.
	if s.agents != nil && s.agents.Count() > 0 {
		s.runScanViaAgent(ctx, job)
		return
	}
	s.runScanLocally(ctx, job)
}

func (s *Service) runScanViaAgent(ctx context.Context, job *ScanJob) {
	agent := s.agents.Pick()
	if agent == nil {
		_, _ = s.failJob(job, "no agents available")
		return
	}
	s.mu.Lock()
	s.scanAgents[job.ID] = agent.id
	s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		s.finishScanContext(job, err)
		return
	}

	cmd := "scan " + strings.Join(scanArgsForJob(job), " ")
	args, _ := aop.JSONValue(map[string]any{"command": cmd})
	resultCh, err := s.agents.DispatchToolCall(agent.id, job.ID, &aop.ToolCall{
		Id: job.ID, Name: "bash", Kind: "function", Arguments: args,
	})
	if err != nil {
		_, _ = s.failJob(job, err.Error())
		return
	}

	// Progress lines stream to the SSE hub as tool.data events while the scan
	// runs; the terminal tool.result carries the full text and the structured
	// scan result in its details.
	var res taskResult
	var ok bool
	select {
	case <-ctx.Done():
		_ = s.agents.CancelTask(agent.id, job.ID)
		s.finishScanContext(job, ctx.Err())
		return
	case res, ok = <-resultCh:
	}
	if ctx.Err() != nil {
		_ = s.agents.CancelTask(agent.id, job.ID)
		s.finishScanContext(job, ctx.Err())
		return
	}
	if !ok {
		_, _ = s.failJob(job, "agent disconnected")
		return
	}
	if res.Err != "" {
		_, _ = s.failJob(job, res.Err)
		return
	}
	if progress := lastOutputLine(res.Output); progress != "" {
		job.Progress = progress
	}

	result, err := decodeScanResult(res.Result)
	if err != nil {
		_, _ = s.failJob(job, err.Error())
		return
	}

	_, _ = s.completeJob(context.Background(), job, agent.id, result)
}

func (s *Service) runScanLocally(ctx context.Context, job *ScanJob) {
	streamWriter := &scanStreamWriter{
		hub:    s.hub,
		scanID: job.ID,
		store:  s.store,
		job:    job,
		ctx:    ctx,
	}

	args := scanArgsForJob(job)
	_, result, err := s.executeScan(ctx, args, streamWriter)
	if err != nil {
		s.finishScanContext(job, ctx.Err())
		if ctx.Err() == nil {
			_, _ = s.failJob(job, err.Error())
		}
		return
	}
	if streamWriter.job != nil {
		job = streamWriter.job
	}
	if ctx.Err() != nil {
		s.finishScanContext(job, ctx.Err())
		return
	}

	_, _ = s.completeJob(context.Background(), job, "", result)
}

func decodeScanResult(raw json.RawMessage) (*output.Result, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("agent scan returned an empty result envelope")
	}
	var result *output.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode agent scan result: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("agent scan returned a null result envelope")
	}
	return result, nil
}

func (s *Service) finishScanContext(job *ScanJob, err error) {
	if err == nil {
		return
	}
	if err == context.DeadlineExceeded {
		_, _ = s.failJob(job, "scan timed out")
		return
	}
	next := *job
	next.Status = StatusCanceled
	next.UpdatedAt = time.Now()
	_, _ = s.store.TransitionScan(context.Background(), &next, StatusQueued, StatusRunning)
}

func (s *Service) persistResultRecords(scanID, agentID string, result *output.Result) {
	recs := resultToRecords(scanID, agentID, result)
	if len(recs) > 0 {
		_ = s.store.InsertRecords(context.Background(), recs)
	}
}

func (s *Service) completeJob(ctx context.Context, job *ScanJob, agentID string, result *output.Result) (bool, error) {
	if result == nil {
		return false, fmt.Errorf("scan result is required")
	}
	next := *job
	next.Status = StatusCompleted
	next.Report = buildMarkdownReport(job.Target, job.Mode, result, defaultReportLang)
	next.Result = result
	next.Error = ""
	next.UpdatedAt = time.Now()
	changed, err := s.store.TransitionScan(ctx, &next, StatusRunning)
	if err != nil || !changed {
		return changed, err
	}
	*job = next
	s.persistResultRecords(job.ID, agentID, result)
	if len(result.Nodes) > 0 {
		_ = s.store.UpsertSCONodes(ctx, job.ID, result.Nodes)
	}
	s.hub.BroadcastScan(scanCompletedEvent(job.ID, result), true)
	s.broadcastScanComplete(job.ID)
	return true, nil
}

func (s *Service) failJob(job *ScanJob, errMsg string) (bool, error) {
	next := *job
	next.Status = StatusFailed
	next.Error = errMsg
	next.UpdatedAt = time.Now()
	changed, err := s.store.TransitionScan(context.Background(), &next, StatusQueued, StatusRunning)
	if err != nil || !changed {
		return changed, err
	}
	*job = next
	s.hub.BroadcastScan(scanFailedEvent(job.ID, errMsg, false), true)
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

func scanArgsForJob(job *ScanJob) []string {
	args := []string{"-i", job.Target, "--mode", job.Mode}
	if job.Verify {
		args = append(args, "--verify=high")
	}
	if job.Sniper {
		args = append(args, "--sniper")
	}
	if job.Deep {
		args = append(args, "--deep")
	}
	return args
}

func (s *Service) executeScan(ctx context.Context, args []string, stream io.Writer) (string, *output.Result, error) {
	app, release := s.acquireApp()
	defer release()
	if app == nil || app.Commands == nil {
		return "", nil, fmt.Errorf("aiscan runtime is not ready")
	}
	tool, ok := app.Commands.GetTool("bash")
	if !ok {
		return "", nil, fmt.Errorf("bash tool is not registered")
	}
	bash, ok := tool.(*commands.BashTool)
	if !ok {
		return "", nil, fmt.Errorf("registered bash tool has unexpected type")
	}
	var text strings.Builder
	execution, err := bash.RunForeground(ctx, commands.JoinCommandLine("scan", args), commands.BashExecOptions{
		OnOutput: func(data []byte) {
			_, _ = text.Write(data)
			if stream != nil {
				_, _ = stream.Write(data)
			}
		},
	})
	if err != nil {
		return text.String(), nil, err
	}
	result, ok := execution.Details.(*output.Result)
	if !ok || result == nil {
		return text.String(), nil, fmt.Errorf("scan execution returned no structured result")
	}
	return text.String(), result, nil
}

type scanStreamWriter struct {
	hub    *Hub
	scanID string
	store  *SQLiteStore
	job    *ScanJob
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
		if current.Status == StatusCanceled {
			return 0, context.Canceled
		}
		current.Progress = line
		current.UpdatedAt = time.Now()
		changed, err := w.store.TransitionScan(context.Background(), current, StatusRunning)
		if err != nil {
			return 0, err
		}
		if !changed {
			return 0, context.Canceled
		}
		w.job = current

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

func (s *Service) HandleFileUpload(ctx context.Context, sessionID, filename string, data []byte) (*transport.FileResult, error) {
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
	agentID := session.AgentID
	if agentID == "" {
		return nil, fmt.Errorf("session has no assigned agent")
	}

	taskID := generateID()
	resultCh, err := s.agents.dispatchFrame(agentID, taskID, &transport.ServerFrame{
		CorrelationId: taskID,
		Payload: &transport.ServerFrame_FileUpload{FileUpload: &transport.FileUploadRequest{
			TaskId: taskID, SessionId: sessionID, Filename: filename,
			MediaType: http.DetectContentType(data), Data: data,
		}},
	})
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

func (s *Service) CreateSession(ctx context.Context, agentID, title string) (*ChatSession, error) {
	var agentName string
	if s.agents != nil {
		if info := s.agents.get(agentID); info != nil {
			agentName = info.name
		}
	}
	now := time.Now()
	session := &ChatSession{
		ID:        generateID(),
		AgentID:   agentID,
		AgentName: agentName,
		Title:     title,
		Status:    SessionActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.CreateSession(ctx, session); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return session, nil
}

func (s *Service) GetSession(ctx context.Context, id string) (*ChatSession, error) {
	return s.store.GetSession(ctx, id)
}

func (s *Service) ListSessions(ctx context.Context) ([]*ChatSession, error) {
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
func (s *Service) SessionMenu(sessionID string) []*transport.CommandSpec {
	hubSpecs := []*transport.CommandSpec{
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
	for _, a := range agents {
		status := "idle"
		if a.Busy {
			status = "busy"
		}
		sb.WriteString(fmt.Sprintf("- **%s** (%s) — %s", a.Name, a.ID[:8], status))
		entry := map[string]any{"name": a.Name, "id": a.ID[:8], "busy": a.Busy}
		if a.Status.Model != "" {
			sb.WriteString(fmt.Sprintf(" — %s/%s", a.Status.Provider, a.Status.Model))
			entry["provider"] = a.Status.Provider
			entry["model"] = a.Status.Model
		}
		sb.WriteString("\n")
		list = append(list, entry)
	}
	s.broadcastSystemMessage(sessionID, SysAgentsList, sb.String(),
		map[string]any{"count": len(agents), "agents": list})
}

func (s *Service) sessionAgent(sessionID string) *remoteAgent {
	session, err := s.store.GetSession(context.Background(), sessionID)
	if err != nil || session.AgentID == "" {
		return nil
	}
	if s.agents == nil {
		return nil
	}
	return s.agents.get(session.AgentID)
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
	if request.RequestId == "" {
		request.RequestId = taskID
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
	resultCh, err := s.agents.DispatchCommand(agent.id, &transport.CommandRequest{TaskId: taskID, SessionId: sessionID, Line: line})
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
	if err != nil || s.agents == nil || session.AgentID == "" {
		return
	}
	requestID := "close:" + sessionID
	_ = s.agents.sendAgentFrame(session.AgentID, &transport.ServerFrame{
		CorrelationId: requestID,
		Payload: &transport.ServerFrame_CloseSession{CloseSession: &aop.CloseSessionRequest{
			RequestId: requestID, SessionId: sessionID, Reason: "completed",
		}},
	})
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
		metadata, _ := json.Marshal(map[string]any{"code": code, "params": params})
		_ = ext.SetWebMessage(event, ext.WebMessageExtension{Metadata: metadata})
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
	value, err := aop.ProtoJSONValue(&scanpb.SessionScanEvent{ScanId: scanID, Status: scanpb.ScanStatus_SCAN_STATUS_COMPLETED})
	if err != nil {
		return
	}
	s.BroadcastAOPEvent(sid, &aop.Event{
		SessionId: sid,
		Emitter:   "aiscan.web",
		Payload: &aop.Event_Extension{Extension: &aop.ExtensionEvent{
			Type: "io.chainreactors.aiscan.scan", Value: value,
		}},
	})
}
