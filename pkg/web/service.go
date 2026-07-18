package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/runner"
	scantool "github.com/chainreactors/aiscan/pkg/tools/scan"
	"github.com/chainreactors/aiscan/pkg/tui"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

// hubCommands are the 3 commands that run on the web hub, not the agent.
var hubCommands = map[string]bool{"scan": true, "agents": true, "help": true}

type ConfigStore interface {
	GetDistributeConfig(ctx context.Context) (path string, loaded bool, cfg webproto.DistributeConfig, err error)
	SaveDistributeConfig(ctx context.Context, cfg webproto.DistributeConfig) error
}

type ServiceConfig struct {
	Store         *SQLiteStore
	App           *runner.App
	ConfigStore   ConfigStore
	AppFactory    func(ctx context.Context) (*runner.App, error)
	AgentPool     *AgentPool
	MaxConcurrent int
	ScanTimeout   time.Duration
}

type Service struct {
	store   *SQLiteStore
	appMu   sync.RWMutex
	app     *runner.App
	config  ConfigStore
	reload  func(ctx context.Context) (*runner.App, error)
	agents  *AgentPool
	hub     *Hub
	sem     chan struct{}
	timeout time.Duration

	mu           sync.Mutex
	cancels      map[string]context.CancelFunc
	taskSessions map[string]string // taskID → sessionID
	taskAgents   map[string]string // taskID → agentID
	taskCanceled map[string]bool
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
		app:          cfg.App,
		config:       cfg.ConfigStore,
		reload:       cfg.AppFactory,
		agents:       cfg.AgentPool,
		hub:          NewHub(),
		sem:          make(chan struct{}, maxConcurrent),
		timeout:      timeout,
		cancels:      make(map[string]context.CancelFunc),
		taskSessions: make(map[string]string),
		taskAgents:   make(map[string]string),
		taskCanceled: make(map[string]bool),
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
	s.appMu.Lock()
	app := s.app
	s.app = nil
	s.appMu.Unlock()
	if app != nil {
		app.Close()
	}
}

func (s *Service) Status() ServiceStatus {
	app := s.appSnapshot()
	status := ServiceStatus{
		Version:      config.Version,
		LLMAvailable: app != nil && app.Provider != nil,
	}
	if app != nil {
		status.LLMProvider = app.ProviderConfig.Provider
		status.LLMModel = app.ProviderConfig.Model
		status.LLMAPIKeyConfigured = strings.TrimSpace(app.ProviderConfig.APIKey) != ""
	}
	if s.config != nil {
		if path, loaded, dc, err := s.config.GetDistributeConfig(context.Background()); err == nil {
			status.ConfigPath = path
			status.ConfigLoaded = loaded
			if status.LLMProvider == "" {
				status.LLMProvider = dc.LLM.Provider
			}
			if status.LLMModel == "" {
				status.LLMModel = dc.LLM.Model
			}
			status.LLMAPIKeyConfigured = status.LLMAPIKeyConfigured || dc.LLM.APIKey != ""
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

func (s *Service) SaveConfig(ctx context.Context, cfg webproto.DistributeConfig) (ConfigStatus, error) {
	if s.config == nil {
		return ConfigStatus{}, fmt.Errorf("config store is not configured")
	}
	if err := s.config.SaveDistributeConfig(ctx, cfg); err != nil {
		return ConfigStatus{}, err
	}
	if s.reload != nil {
		app, err := s.reload(ctx)
		if err != nil {
			cs, _ := s.GetConfigStatus(ctx)
			return cs, fmt.Errorf("reload aiscan runtime: %w", err)
		}
		s.swapApp(app)
	}
	// Tell connected agents to hot-swap their own provider too — the hub reload
	// above only refreshes the hub's in-process runtime, not the agent subprocesses.
	if s.agents != nil {
		s.agents.BroadcastConfigReload()
	}
	return s.GetConfigStatus(ctx)
}

func (s *Service) GetDistributeConfig(ctx context.Context) (webproto.DistributeConfig, error) {
	if s.config == nil {
		return webproto.DistributeConfig{}, fmt.Errorf("config store is not configured")
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

	go s.runScan(job.ID) //nolint:gosec // G118: background scan outlives the request

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
	s.mu.Lock()
	cancel, ok := s.cancels[id]
	s.mu.Unlock()
	if ok {
		cancel()
	}
	ctx := context.Background()
	job, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if job.Status == StatusRunning || job.Status == StatusQueued {
		job.Status = StatusCanceled
		job.UpdatedAt = time.Now()
		return s.store.Update(ctx, job)
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

func (s *Service) runScan(jobID string) {
	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	s.mu.Lock()
	s.cancels[jobID] = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.cancels, jobID)
		s.mu.Unlock()
	}()

	job, err := s.store.Get(ctx, jobID)
	if err != nil {
		return
	}
	if job.Status == StatusCanceled {
		return
	}

	job.Status = StatusRunning
	job.UpdatedAt = time.Now()
	_ = s.store.Update(ctx, job)

	s.hub.Broadcast(jobID, HubEvent{
		Type: "status",
		Data: mustJSON(map[string]string{"scan_id": jobID, "status": string(StatusRunning)}),
	})

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
		s.failJob(job, "no agents available")
		return
	}

	cmd := "scan " + strings.Join(scanArgsForJob(job), " ")
	resultCh, err := s.agents.DispatchCommand(agent.id, job.ID, cmd)
	if err != nil {
		s.failJob(job, err.Error())
		return
	}

	// Wait for agent to complete. Output is forwarded to SSE hub by
	// AgentPool.HandleOutput as the agent POSTs progress lines.
	res, ok := <-resultCh
	if !ok {
		s.failJob(job, "agent disconnected")
		return
	}
	if res.Err != "" {
		s.failJob(job, res.Err)
		return
	}
	if progress := lastOutputLine(res.Output); progress != "" {
		job.Progress = progress
	}

	var result *output.Result
	if len(res.Result) > 0 {
		result = &output.Result{}
		_ = json.Unmarshal(res.Result, result)
	}

	s.completeJob(ctx, job, agent.id, result)
}

func (s *Service) runScanLocally(ctx context.Context, job *ScanJob) {
	streamWriter := &sseStreamWriter{
		hub:    s.hub,
		scanID: job.ID,
		store:  s.store,
		job:    job,
		ctx:    ctx,
	}

	args := scanArgsForJob(job)
	_, result, err := s.executeScan(ctx, args, streamWriter)
	if err != nil {
		s.failJob(job, err.Error())
		return
	}
	if streamWriter.job != nil {
		job = streamWriter.job
	}

	s.completeJob(ctx, job, "", result)
}

func (s *Service) persistResultRecords(scanID, agentID string, result *output.Result) {
	recs := resultToRecords(scanID, agentID, result)
	if len(recs) > 0 {
		_ = s.store.InsertRecords(context.Background(), recs)
	}
}

func (s *Service) completeJob(ctx context.Context, job *ScanJob, agentID string, result *output.Result) {
	job.Status = StatusCompleted
	job.Report = buildMarkdownReport(job.Target, job.Mode, result, defaultReportLang)
	job.Result = result
	job.UpdatedAt = time.Now()
	_ = s.store.Update(ctx, job)
	s.persistResultRecords(job.ID, agentID, result)
	if len(result.Nodes) > 0 {
		_ = s.store.UpsertSCONodes(ctx, job.ID, result.Nodes)
	}
	s.hub.Broadcast(job.ID, HubEvent{
		Type:     "complete",
		Data:     mustJSON(map[string]any{"scan_id": job.ID, "status": "completed", "result": result}),
		Reliable: true,
	})
	s.broadcastScanComplete(job.ID, result)
}

func (s *Service) failJob(job *ScanJob, errMsg string) {
	job.Status = StatusFailed
	job.Error = errMsg
	job.UpdatedAt = time.Now()
	_ = s.store.Update(context.Background(), job)
	s.hub.Broadcast(job.ID, HubEvent{
		Type:     "error",
		Data:     mustJSON(map[string]string{"scan_id": job.ID, "error": errMsg}),
		Reliable: true,
	})
}

func (s *Service) aiAvailable() bool {
	app := s.appSnapshot()
	return app != nil && app.Provider != nil
}

func (s *Service) appSnapshot() *runner.App {
	if s == nil {
		return nil
	}
	s.appMu.RLock()
	defer s.appMu.RUnlock()
	return s.app
}

func (s *Service) swapApp(next *runner.App) {
	if s == nil || next == nil {
		return
	}
	s.appMu.Lock()
	prev := s.app
	s.app = next
	s.appMu.Unlock()
	if prev != nil && prev != next {
		prev.Close()
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

type structuredScanCommand interface {
	ExecuteStructured(ctx context.Context, args []string, stream io.Writer) (string, *output.Result, error)
}

func (s *Service) executeScan(ctx context.Context, args []string, stream io.Writer) (string, *output.Result, error) {
	app := s.appSnapshot()
	if app == nil || app.Commands == nil {
		return "", nil, fmt.Errorf("aiscan runtime is not ready")
	}
	cmd, ok := app.Commands.Get("scan")
	if !ok {
		return "", nil, fmt.Errorf("scan command is not registered")
	}
	structured, ok := cmd.(structuredScanCommand)
	if !ok {
		return "", nil, fmt.Errorf("scan command does not support structured results")
	}
	return structured.ExecuteStructured(ctx, args, stream)
}

type sseStreamWriter struct {
	hub    *Hub
	scanID string
	store  *SQLiteStore
	job    *ScanJob
	ctx    context.Context
	buf    []byte
}

func (w *sseStreamWriter) Write(p []byte) (int, error) {
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
		if err := w.store.Update(context.Background(), current); err != nil {
			return 0, err
		}
		w.job = current

		w.hub.Broadcast(w.scanID, HubEvent{
			Type: "progress",
			Data: mustJSON(map[string]string{"scan_id": w.scanID, "data": line}),
		})
	}
	return len(p), nil
}

// defaultReportLang is the language the report is frozen in at scan time; the
// stored copy is only a fallback — GetReport re-renders per request language.
const defaultReportLang = "zh"

// reportWriter holds the target language so every helper can call w.tr()
// without threading `lang` through every signature.
type reportWriter struct {
	strings.Builder
	lang string
}

func newReportWriter(lang string) *reportWriter {
	if strings.HasPrefix(strings.ToLower(lang), "zh") {
		return &reportWriter{lang: "zh"}
	}
	return &reportWriter{lang: "en"}
}

func (w *reportWriter) tr(zh, en string) string {
	if w.lang == "zh" {
		return zh
	}
	return en
}

func (w *reportWriter) modeName(mode string) string {
	if strings.EqualFold(mode, "full") {
		return w.tr("全面侦察", "Full recon")
	}
	return w.tr("快速侦察", "Quick recon")
}

func (w *reportWriter) sep() string { return w.tr("：", ": ") }

// buildMarkdownReport renders a scan result as an operator-facing recon report.
// It reads like something a human wrote — a prose overview instead of a raw
// metric dump, no internal scanner names (gogo_portscan / check) leaking into
// the prose, and bare live hosts (an icmp echo, say) folded into a trailing
// list rather than each claiming a full section.
func buildMarkdownReport(target, mode string, result *output.Result, lang string) string {
	w := newReportWriter(lang)

	heading := output.FirstNonEmpty(target, w.tr("目标", "target"))
	fmt.Fprintf(w, "# %s%s\n\n", w.tr("侦察报告 · ", "Recon report · "), heading)
	fmt.Fprintf(w, "%s `%s`  ·  %s  ·  %s\n\n",
		w.tr("目标", "Target"), target,
		w.modeName(mode),
		time.Now().Format("2006-01-02 15:04:05"))
	w.WriteString("---\n\n")

	if result == nil {
		w.WriteString(w.tr("本次扫描未返回结构化结果。\n", "No structured result was returned.\n"))
		return w.String()
	}

	w.WriteString("## " + w.tr("概述", "Overview") + "\n\n")
	w.writeOverview(result)
	w.WriteString("\n\n")

	rich, bare := splitReportAssets(result.Assets)
	if len(rich) > 0 {
		w.WriteString("## " + w.tr("资产明细", "Assets") + "\n\n")
		for _, asset := range rich {
			w.writeAsset(asset)
		}
	}
	if len(bare) > 0 {
		w.WriteString("## " + w.tr("其他存活主机", "Other live hosts") + "\n\n")
		for _, asset := range bare {
			w.writeBareAsset(asset)
		}
		w.WriteString("\n")
	}

	return w.String()
}

// writeOverview appends the executive summary — one flowing paragraph that
// names only the numbers that are actually present, so a clean scan reads like
// a sentence rather than a table full of zeros.
func (w *reportWriter) writeOverview(result *output.Result) {
	s := result.Summary
	hosts := reportHostCount(result.Assets)
	fingers := resultFingerprintCount(result)

	if w.lang == "zh" {
		fmt.Fprintf(w, "本次侦察共识别 %d 台主机、%d 个开放服务", hosts, s.Services)
		if s.Webs > 0 {
			fmt.Fprintf(w, "（含 %d 个 Web 站点）", s.Webs)
		}
		w.WriteString("。")
		if s.Probes > 0 {
			fmt.Fprintf(w, "累计探测 %d 条路径", s.Probes)
			if fingers > 0 {
				fmt.Fprintf(w, "、命中 %d 项 Web 指纹", fingers)
			}
			w.WriteString("。")
		} else if fingers > 0 {
			fmt.Fprintf(w, "命中 %d 项 Web 指纹。", fingers)
		}
		if s.Loots > 0 {
			fmt.Fprintf(w, "**发现 %d 项需优先复核的安全发现（凭证 / 弱口令 / 漏洞）。**", s.Loots)
		}
		if s.Errors > 0 {
			fmt.Fprintf(w, "另有 %d 处探测报错。", s.Errors)
		}
		if s.Duration != "" {
			fmt.Fprintf(w, "全程耗时 %s。", s.Duration)
		}
		return
	}

	fmt.Fprintf(w, "The scan identified %s across %s", plural(hosts, "host", "hosts"), plural(s.Services, "open service", "open services"))
	if s.Webs > 0 {
		fmt.Fprintf(w, " (%s)", plural(s.Webs, "web site", "web sites"))
	}
	w.WriteString(". ")
	if s.Probes > 0 {
		fmt.Fprintf(w, "It probed %s", plural(s.Probes, "path", "paths"))
		if fingers > 0 {
			fmt.Fprintf(w, " and matched %s", plural(fingers, "fingerprint", "fingerprints"))
		}
		w.WriteString(". ")
	} else if fingers > 0 {
		fmt.Fprintf(w, "It matched %s. ", plural(fingers, "fingerprint", "fingerprints"))
	}
	if s.Loots > 0 {
		fmt.Fprintf(w, "**%s surfaced (credentials / weak passwords / vulnerabilities) — review these first.** ", plural(s.Loots, "security finding", "security findings"))
	}
	if s.Errors > 0 {
		fmt.Fprintf(w, "%s occurred during probing. ", plural(s.Errors, "error", "errors"))
	}
	if s.Duration != "" {
		fmt.Fprintf(w, "The scan took %s.", s.Duration)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// reportHostCount collapses assets down to distinct hosts, so an IP that has
// both an icmp echo and a web service counts once, not twice.
func reportHostCount(assets []output.Asset) int {
	seen := make(map[string]struct{})
	for _, a := range assets {
		if h := assetHost(a); h != "" {
			seen[h] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return len(assets)
	}
	return len(seen)
}

func assetHost(a output.Asset) string {
	v := output.FirstNonEmpty(a.Target, a.Key, a.Title)
	if i := strings.Index(v, "://"); i >= 0 {
		v = v[i+3:]
	}
	if i := strings.IndexAny(v, "/?#"); i >= 0 {
		v = v[:i]
	}
	if strings.Count(v, ":") == 1 { // host:port — drop the port, leave IPv6 alone
		v = v[:strings.LastIndex(v, ":")]
	}
	return v
}

func splitReportAssets(assets []output.Asset) (rich, bare []output.Asset) {
	for _, a := range assets {
		if assetIsBare(a) {
			bare = append(bare, a)
		} else {
			rich = append(rich, a)
		}
	}
	return rich, bare
}

// assetIsBare is true for a live host that only answered with non-web services
// (an icmp echo, a bare tcp port) — nothing worth its own section.
func assetIsBare(a output.Asset) bool {
	hasService := false
	for _, item := range a.Items {
		if item.Kind != output.AssetItemService {
			return false
		}
		hasService = true
		svc := strings.ToLower(output.AssetDataString(item.Data, "service") + " " + output.AssetDataString(item.Data, "protocol"))
		if strings.Contains(svc, "http") {
			return false
		}
	}
	return hasService
}

func (w *reportWriter) writeAsset(asset output.Asset) {
	title := output.FirstNonEmpty(asset.Title, asset.Target, asset.Key, w.tr("资产", "Asset"))
	if asset.Target != "" && asset.Target != title {
		fmt.Fprintf(w, "### %s — `%s`\n\n", title, asset.Target)
	} else {
		fmt.Fprintf(w, "### %s\n\n", title)
	}

	w.writeFact(w.tr("开放服务", "Services"), assetServiceFacts(asset.Items))
	w.writeFact(w.tr("HTTP 响应", "HTTP"), assetHTTPStatuses(asset.Items))
	w.writeFact(w.tr("Web 指纹", "Fingerprints"), assetFingers(asset.Items))
	if paths := assetPathCount(asset.Items); paths > 0 {
		fmt.Fprintf(w, "- %s%s%s\n", w.tr("已探测路径", "Paths"), w.sep(), w.tr(fmt.Sprintf("%d 条", paths), fmt.Sprintf("%d", paths)))
	}
	if asset.Status != "" {
		fmt.Fprintf(w, "- %s%s%s\n", w.tr("状态", "State"), w.sep(), markdownCode(asset.Status))
	}
	w.WriteString("\n")

	w.writeLootMarkdown(asset.Items)
}

func (w *reportWriter) writeBareAsset(asset output.Asset) {
	host := output.FirstNonEmpty(asset.Target, asset.Title, asset.Key)
	if services := assetServiceFacts(asset.Items); len(services) > 0 {
		fmt.Fprintf(w, "- `%s` · %s\n", host, strings.Join(services, ", "))
	} else {
		fmt.Fprintf(w, "- `%s`\n", host)
	}
}

func (w *reportWriter) writeFact(label string, values []string) {
	if len(values) == 0 {
		return
	}
	coded := make([]string, 0, len(values))
	for _, value := range values {
		coded = append(coded, markdownCode(value))
	}
	fmt.Fprintf(w, "- %s%s%s\n", label, w.sep(), strings.Join(coded, w.tr("、", ", ")))
}

func (w *reportWriter) writeLootMarkdown(items []output.AssetItem) {
	wrote := false
	for _, item := range items {
		switch item.Kind {
		case output.AssetItemLoot, output.AssetItemNote, output.AssetItemResponse, output.AssetItemError:
			summary := output.FirstNonEmpty(item.Summary, item.Title)
			detail := output.AssetItemDetail(item)
			if summary == "" && detail == "" {
				continue
			}
			if !wrote {
				w.WriteString("#### " + w.tr("分析研判", "Analysis") + "\n\n")
				wrote = true
			}
			if summary == "" {
				summary = firstMarkdownLine(detail)
			}
			fmt.Fprintf(w, "##### %s\n\n", markdownHeading(summary))
			if detail != "" && !sameMarkdownText(summary, detail) {
				writeMarkdownBlock(&w.Builder, detail)
			} else if detail == "" && summary != "" {
				w.WriteString(summary)
				w.WriteString("\n\n")
			}
		}
	}
}

func firstMarkdownLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if idx := strings.IndexByte(value, '\n'); idx >= 0 {
		return strings.TrimSpace(value[:idx])
	}
	return value
}

func sameMarkdownText(left, right string) bool {
	return strings.TrimSpace(left) == strings.TrimSpace(right)
}

func writeMarkdownBlock(sb *strings.Builder, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	sb.WriteString(value)
	sb.WriteString("\n\n")
}

func assetServiceFacts(items []output.AssetItem) []string {
	var values []string
	for _, item := range items {
		if item.Kind != output.AssetItemService {
			continue
		}
		values = append(values, strings.Join(output.CompactStrings(
			output.AssetDataString(item.Data, "protocol"),
			output.AssetDataString(item.Data, "service"),
			output.AssetDataString(item.Data, "port"),
		), " "))
	}
	return output.CompactStrings(values...)
}

func assetHTTPStatuses(items []output.AssetItem) []string {
	var values []string
	for _, item := range items {
		if item.Kind == output.AssetItemPath && item.Status != "" {
			values = append(values, item.Status)
		}
	}
	return output.CompactStrings(values...)
}

func assetFingers(items []output.AssetItem) []string {
	var values []string
	for _, item := range items {
		switch item.Kind {
		case output.AssetItemFingerprint:
			values = append(values, output.FirstNonEmpty(item.Title, output.AssetDataString(item.Data, "name")))
		case output.AssetItemPath:
			values = append(values, output.AssetDataStrings(item.Data, "fingers")...)
		}
	}
	return output.CompactStrings(values...)
}

func assetPathCount(items []output.AssetItem) int {
	count := 0
	for _, item := range items {
		if item.Kind == output.AssetItemPath {
			count++
		}
	}
	return count
}

func resultFingerprintCount(result *output.Result) int {
	if result == nil {
		return 0
	}
	seen := make(map[string]struct{})
	for _, asset := range result.Assets {
		for _, finger := range assetFingers(asset.Items) {
			seen[strings.ToLower(finger)] = struct{}{}
		}
	}
	return len(seen)
}

func markdownCode(value string) string {
	value = strings.ReplaceAll(value, "`", "'")
	return "`" + value + "`"
}

func markdownHeading(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\n", " ")
	if value == "" {
		return "Analysis"
	}
	return strings.TrimLeft(value, "# ")
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

func sessionTopic(id string) string {
	return "session:" + id
}

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
				s.agents.CancelTask(task.agentID, task.taskID)
			}
		}
	}
	s.broadcastSystemMessage(sessionID, SysPaused, "Paused.", nil)
	return nil
}

func (s *Service) HandleFileUpload(ctx context.Context, sessionID, filename string, data []byte) (*webproto.FileUploadResult, error) {
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}
	if s.agents == nil {
		return nil, fmt.Errorf("no agent pool available")
	}
	agentID := session.AgentID
	if agentID == "" {
		return nil, fmt.Errorf("session has no assigned agent")
	}

	payload := webproto.FileUploadPayload{
		Filename:  filename,
		FileSize:  int64(len(data)),
		MimeType:  http.DetectContentType(data),
		SessionID: sessionID,
	}
	payloadJSON, _ := json.Marshal(payload)

	taskID := generateID()
	msg := WSMessage{
		Type:    "upload",
		TaskID:  taskID,
		DataB64: base64.StdEncoding.EncodeToString(data),
		Payload: payloadJSON,
	}

	resultCh, err := s.agents.dispatchMessage(agentID, taskID, msg)
	if err != nil {
		return nil, fmt.Errorf("agent dispatch failed: %w", err)
	}

	select {
	case res, ok := <-resultCh:
		if !ok {
			return nil, fmt.Errorf("agent disconnected during upload")
		}
		var result webproto.FileUploadResult
		// The agent normally returns a JSON-encoded FileUploadResult. If it sent
		// nothing structured (or non-JSON output), synthesize one from the raw
		// output path — the upload still succeeded, just without an envelope.
		if len(res.Result) == 0 || json.Unmarshal(res.Result, &result) != nil {
			result = webproto.FileUploadResult{
				Filename: filename,
				Path:     res.Output,
				Size:     int64(len(data)),
			}
		}
		if result.Error != "" {
			return nil, fmt.Errorf("agent upload error: %s", result.Error)
		}
		s.broadcastSystemMessage(sessionID, SysFileUploaded,
			fmt.Sprintf("File uploaded: %s → %s", filename, result.Path),
			map[string]any{"filename": filename, "path": result.Path})
		return &result, nil
	case <-ctx.Done():
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
	return s.store.DeleteSession(ctx, id)
}

func (s *Service) GetMessages(ctx context.Context, sessionID string) ([]*ChatMessage, error) {
	return s.store.ListMessages(ctx, sessionID, 500)
}

func (s *Service) BroadcastChatEvent(sessionID string, event ChatEvent) {
	event.SessionID = sessionID
	if !event.Transient {
		s.persistRuntimeChatEvent(sessionID, event)
	}
	s.hub.Broadcast(sessionTopic(sessionID), HubEvent{
		Type: event.Type,
		Data: mustJSON(event),
		// Terminal events must never be dropped (see isTerminalChatEvent). Eval
		// verdicts are rare, non-terminal, but each one is a discrete round marker
		// the client can't reconstruct if lost under backpressure — send reliably.
		Reliable: isTerminalChatEvent(event.Type) || event.Type == ChatEventEval || event.Type == ChatEventCompact,
	})
}

// isTerminalChatEvent reports whether an event ends a run (or its scan) on the
// client — the signals that release the composer and stop the streaming
// indicators. These are broadcast reliably so the SSE hub never drops them under
// backpressure: a lost token delta is invisible (a later delta and the final
// message resend the full text), but a lost terminal event leaves the UI stuck
// "streaming" forever — a busy composer and a blinking cursor that never clears.
func isTerminalChatEvent(t string) bool {
	switch t {
	case ChatEventMessage, ChatEventMessageEnd, ChatEventError,
		ChatEventScanComplete, ChatEventScanError:
		return true
	}
	return false
}

func (s *Service) persistRuntimeChatEvent(sessionID string, event ChatEvent) {
	if s == nil || s.store == nil || sessionID == "" {
		return
	}

	now := time.Now()
	msg := &ChatMessage{
		ID:        generateID(),
		SessionID: sessionID,
		AgentID:   event.AgentID,
		AgentName: event.AgentName,
		CreatedAt: now,
	}
	metadata := map[string]any{
		"event_type": event.Type,
	}
	if event.Turn > 0 {
		metadata["turn"] = event.Turn
	}

	switch event.Type {
	case ChatEventMessageEnd:
		// The finalized assistant text for one turn — the commentary the model
		// emits before (or between) its tool calls. Only the run's LAST turn used
		// to survive a reload, persisted as the aggregate reply by
		// completeAssistantRun; every earlier turn's text streamed live but was
		// dropped from the store, so it vanished from any timeline rebuilt from it:
		// a page reload, an SSE reconnect, or a session switch that revalidates
		// against the store. Persist each turn's text as an assistant message keyed
		// by its turn so buildTimelineFromMessages reconstructs it. The final turn
		// shares a turn key with the aggregate reply, so on rebuild the two merge
		// into one bubble instead of doubling. (message_start / message_delta stay
		// unpersisted — they are streaming partials of this same finalized text.)
		msg.Role = "assistant"
		msg.Content = strings.TrimSpace(event.Content)
		if msg.Content == "" {
			return
		}

	case ChatEventThinking:
		msg.Role = "system"
		msg.Content = strings.TrimSpace(event.Content)
		if msg.Content == "" {
			msg.Content = "thinking"
		}

	case ChatEventAgentJoined:
		msg.Role = "system"
		msg.Content = strings.TrimSpace(event.AgentName + " joined")

	case ChatEventToolCall:
		msg.Role = "tool_call"
		msg.Content = event.ToolArgs
		metadata["tool_call_id"] = event.ToolCallID
		metadata["tool_name"] = event.ToolName
		metadata["tool_args"] = event.ToolArgs

	case ChatEventToolResult:
		msg.Role = "tool_result"
		msg.Content = event.Content
		metadata["tool_call_id"] = event.ToolCallID

	case ChatEventEval:
		// Persist the round verdict so the eval badge survives a reload / session
		// switch — buildTimelineFromMessages reconstructs it from content (reason)
		// plus this metadata (round/pass).
		msg.Role = "system"
		msg.Content = event.EvalReason
		metadata["eval_round"] = event.EvalRound
		metadata["eval_pass"] = event.EvalPass
		metadata["eval_reason"] = event.EvalReason

	case ChatEventCompact:
		msg.Role = "system"
		msg.Content = fmt.Sprintf("context compacted: ~%d → ~%d tokens", event.CompactTokensBefore, event.CompactTokensAfter)
		metadata["compact_tokens_before"] = event.CompactTokensBefore
		metadata["compact_tokens_after"] = event.CompactTokensAfter
		metadata["compact_kept_messages"] = event.CompactKeptMessages

	case ChatEventScanComplete:
		// Persist a lightweight marker so the inline scan card survives a reload /
		// session switch. The heavy Result payload is NOT stored here — it stays
		// reloadable via the session_scans link (getScan), and the client fills the
		// card from its scanResults map keyed by this scan_id. Without this marker
		// the scan is invisible to any timeline rebuilt from messages (a page
		// reload, an SSE reconnect, or a session switch that revalidates against
		// the store), even though the result itself is still fetchable.
		if event.ScanID == "" {
			return
		}
		msg.Role = "system"
		msg.Content = "scan complete"
		metadata["scan_id"] = event.ScanID

	default:
		return
	}

	if data, err := json.Marshal(metadata); err == nil {
		msg.Metadata = data
	}
	_ = s.store.AddMessage(context.Background(), msg)
}

func (s *Service) HandleUserMessage(ctx context.Context, sessionID, content string, opts webproto.ChatPayload) (*ChatMessage, error) {
	now := time.Now()
	msg := &ChatMessage{
		ID:        generateID(),
		SessionID: sessionID,
		Role:      "user",
		Content:   content,
		CreatedAt: now,
	}
	if err := s.store.AddMessage(ctx, msg); err != nil {
		return nil, fmt.Errorf("store message: %w", err)
	}

	// Update session timestamp and auto-title from first message.
	session, err := s.store.GetSession(ctx, sessionID)
	if err == nil {
		session.UpdatedAt = now
		if session.Title == "" {
			title := content
			if len(title) > 60 {
				title = title[:60] + "..."
			}
			session.Title = title
		}
		_ = s.store.UpdateSession(ctx, session)
	}

	go s.dispatchUserMessage(sessionID, msg, opts)

	return msg, nil
}

func (s *Service) dispatchUserMessage(sessionID string, msg *ChatMessage, opts webproto.ChatPayload) {
	content := strings.TrimSpace(msg.Content)

	// A typed "/verb" is routed by scope. Hub-scope commands (scan pipeline,
	// agent roster, merged help) run here. Agent-scope commands (/status,
	// /provider, /<skill>, ...) and unknown verbs fall through to the agent,
	// where the AgentConsole bridge runs the real REPL — so the full REPL
	// command set and `!bash` work from the browser without a parallel switch.
	if verb, args, ok := parseCommand(content); ok {
		// /clear is a true "clear conversation" on the web: it must wipe the
		// visible+persisted transcript, not just reset the agent's model context.
		// Owned end-to-end by the hub so it does both (see handleClearCommand).
		if verb == "clear" {
			s.handleClearCommand(sessionID, opts)
			return
		}
		if hubCommands[verb] {
			s.runHubCommand(sessionID, verb, args)
			return
		}
	}

	s.handleChatMessage(sessionID, content, opts)
}

// handleClearCommand implements web /clear as "clear conversation": it deletes the
// session's persisted messages (incl. the "/clear" message itself) and signals the
// open UI to empty its timeline, then forwards /clear to the bound agent so its
// in-memory model context resets too. The agent's "Context cleared." reply lands in
// the now-empty transcript as the sole confirmation line; with no agent bound, the
// emptied view is itself the confirmation.
func (s *Service) handleClearCommand(sessionID string, opts webproto.ChatPayload) {
	_ = s.store.ClearMessages(context.Background(), sessionID)
	// Transient: a live-only signal to connected clients — the cleared state is
	// already durable in the store, so a reconnecting client re-derives it on load.
	s.BroadcastChatEvent(sessionID, ChatEvent{Type: ChatEventSessionCleared, Transient: true})
	if s.sessionAgent(sessionID) != nil {
		s.handleChatMessage(sessionID, "/clear", opts)
	}
}

// runHubCommand executes a hub-scope slash command — one that needs hub state
// (the scan pipeline, the connected-agent roster, or the merged help catalog).
// name is the canonical catalog name without its leading slash. Agent-scope
// commands never reach here; they fall through to the agent bridge.
func (s *Service) runHubCommand(sessionID, name, args string) {
	switch name {
	case "scan":
		s.handleScanCommand(sessionID, args)
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
// single source both the "/" menu (GET .../commands) and /help render from.
func (s *Service) SessionMenu(sessionID string) []webproto.CommandSpec {
	hubSpecs := []webproto.CommandSpec{
		{Name: "/help", Description: "查看命令面板"},
		{Name: "/scan", Description: "在本会话运行扫描", Usage: "/scan <target> [--mode full] [--verify] [--sniper] [--deep]"},
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

func (s *Service) handleScanCommand(sessionID, args string) {
	ctx := context.Background()
	parts := strings.Fields(args)
	if len(parts) == 0 {
		s.BroadcastChatEvent(sessionID, ChatEvent{
			Type:  ChatEventError,
			Error: "usage: /scan <target> [--mode full] [--verify] [--sniper] [--deep]",
		})
		return
	}

	target := parts[0]
	mode := "quick"
	var verify, sniper, deep bool
	for _, p := range parts[1:] {
		switch p {
		case "--mode":
			// next arg handled below
		case "full":
			mode = "full"
		case "--verify":
			verify = true
		case "--sniper":
			sniper = true
		case "--deep":
			deep = true
		}
	}
	for i, p := range parts {
		if p == "--mode" && i+1 < len(parts) {
			mode = parts[i+1]
		}
	}

	job, err := s.SubmitScan(ctx, target, mode, verify, sniper, deep)
	if err != nil {
		s.BroadcastChatEvent(sessionID, ChatEvent{
			Type:  ChatEventError,
			Error: fmt.Sprintf("scan failed: %s", err),
		})
		return
	}

	_ = s.store.LinkScanToSession(ctx, sessionID, job.ID)

	s.registerSessionTask(job.ID, sessionID, "")

	s.BroadcastChatEvent(sessionID, ChatEvent{
		Type:   ChatEventScanStarted,
		ScanID: job.ID,
		Data:   fmt.Sprintf("Scan started: %s (%s)", target, mode),
	})
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
		if a.Identity.Model != "" {
			sb.WriteString(fmt.Sprintf(" — %s/%s", a.Identity.Provider, a.Identity.Model))
			entry["provider"] = a.Identity.Provider
			entry["model"] = a.Identity.Model
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

func (s *Service) handleChatMessage(sessionID, content string, opts webproto.ChatPayload) {
	agent := s.sessionAgent(sessionID)
	if agent == nil {
		s.broadcastSystemMessage(sessionID, SysAgentNotConnected,
			"Agent is not connected. Reconnect the agent to continue chatting.", nil)
		return
	}

	taskID := generateID()
	s.registerSessionTask(taskID, sessionID, agent.id)

	s.BroadcastChatEvent(sessionID, ChatEvent{
		Type:      ChatEventAgentJoined,
		AgentID:   agent.id,
		AgentName: agent.name,
	})

	resultCh, err := s.agents.DispatchChatSession(agent.id, taskID, sessionID, content, opts)
	if err != nil {
		s.finishSessionTask(taskID)
		s.BroadcastChatEvent(sessionID, ChatEvent{
			Type:  ChatEventError,
			Error: err.Error(),
		})
		return
	}

	go func() {
		res, ok := <-resultCh
		canceled := s.finishSessionTask(taskID)
		if !ok {
			// Agent dropped mid-run: signal completion so the composer releases
			// instead of hanging on the streaming indicator (mirrors the command
			// path above).
			s.BroadcastChatEvent(sessionID, ChatEvent{
				Type:  ChatEventError,
				Error: "agent disconnected",
			})
			return
		}
		if canceled {
			return
		}
		reply := res.Output
		if res.Err != "" {
			reply = "Error: " + res.Err
		}
		s.completeAssistantRun(sessionID, agent.id, agent.name, reply, res.Turn)
	}()
}

// broadcastSystemMessage persists + broadcasts a system message. code names a
// translatable template rendered client-side via i18n (see the Sys* codes);
// fallback is the English text kept in Content for non-i18n consumers, logs and
// tests. params feeds i18n interpolation and is stored next to code so the
// message stays localizable after a reload.
func (s *Service) broadcastSystemMessage(sessionID, code, fallback string, params map[string]any) {
	now := time.Now()
	var meta json.RawMessage
	if code != "" {
		meta, _ = json.Marshal(map[string]any{"code": code, "params": params})
	}
	msg := &ChatMessage{
		ID:        generateID(),
		SessionID: sessionID,
		Role:      "system",
		Content:   fallback,
		Metadata:  meta,
		CreatedAt: now,
	}
	_ = s.store.AddMessage(context.Background(), msg)
	s.BroadcastChatEvent(sessionID, ChatEvent{
		Type:      ChatEventMessage,
		MessageID: msg.ID,
		Role:      "system",
		Content:   fallback,
		Code:      code,
		Params:    params,
	})
}

func (s *Service) broadcastScanComplete(scanID string, result *output.Result) {
	s.mu.Lock()
	sid, ok := s.taskSessions[scanID]
	s.mu.Unlock()
	if !ok {
		return
	}
	if s.finishSessionTask(scanID) {
		return
	}
	s.BroadcastChatEvent(sid, ChatEvent{
		Type:   ChatEventScanComplete,
		ScanID: scanID,
		Result: result,
	})
}

// completeAssistantRun is a run's terminal signal to the client. It always
// broadcasts the aggregate assistant message so the UI finalizes the turn and
// releases the composer — even when the run produced no final text (a tool-only
// turn, or an eval run that hit its round cap). Skipping the broadcast on empty
// content was what stranded the streaming indicator — the blinking cursor or the
// "working" dots — forever. The reply is persisted only when it carries text, so
// an empty completion never leaves a blank row in the transcript.
func (s *Service) completeAssistantRun(sessionID, agentID, agentName, content string, turn int) {
	content = strings.TrimRight(content, " \t\r\n")
	event := ChatEvent{
		Type:      ChatEventMessage,
		Role:      "assistant",
		AgentID:   agentID,
		AgentName: agentName,
		Turn:      turn,
		Content:   content,
	}
	if content != "" {
		msg := &ChatMessage{
			ID:        generateID(),
			SessionID: sessionID,
			Role:      "assistant",
			AgentID:   agentID,
			AgentName: agentName,
			Content:   content,
			CreatedAt: time.Now(),
		}
		if turn > 0 {
			if data, err := json.Marshal(map[string]any{"turn": turn}); err == nil {
				msg.Metadata = data
			}
		}
		_ = s.store.AddMessage(context.Background(), msg)
		event.MessageID = msg.ID
	}
	s.BroadcastChatEvent(sessionID, event)
}
