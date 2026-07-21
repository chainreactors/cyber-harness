package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/chainreactors/aiscan/pkg/agent/probe"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

type Handler struct {
	handler http.Handler
}

func NewHandler(service *Service, agents *AgentPool, local *LocalAgents, ioaHandler http.Handler, static http.Handler, accessKey string, ioaConsole ...IOAConsoleReader) *Handler {
	mux := http.NewServeMux()

	var console IOAConsoleReader
	if len(ioaConsole) > 0 {
		console = ioaConsole[0]
	}
	h := &handlerImpl{service: service, agents: agents, ioa: console, accessKey: accessKey}

	mux.HandleFunc("POST /api/scans", h.createScan)
	mux.HandleFunc("GET /api/scans", h.listScans)
	mux.HandleFunc("GET /api/scans/{id}", h.getScan)
	mux.HandleFunc("DELETE /api/scans/{id}", h.cancelScan)
	mux.HandleFunc("GET /api/scans/{id}/events", h.scanEvents)
	mux.HandleFunc("GET /api/scans/{id}/report", h.scanReport)
	mux.HandleFunc("GET /api/status", h.serviceStatus)
	mux.HandleFunc("GET /api/config", h.getConfig)
	mux.HandleFunc("PUT /api/config", h.saveConfig)
	mux.HandleFunc("PUT /api/config/llm/active", h.activateLLMProfile)
	mux.HandleFunc("GET /api/config/distribute", h.getDistributeConfig)
	mux.HandleFunc("POST /api/config/llm/test", h.testLLM)
	mux.HandleFunc("POST /api/config/llm/models", h.listLLMModels)
	mux.HandleFunc("POST /api/config/{section}/test", h.testConn)
	mux.HandleFunc("GET /api/agents", h.listAgents)
	if console != nil {
		mux.HandleFunc("GET /api/ioa/overview", h.ioaOverview)
	}

	mux.HandleFunc("GET /api/sco/nodes", h.listSCONodes)
	mux.HandleFunc("GET /api/sco/nodes/{id}", h.getSCONode)
	mux.HandleFunc("GET /api/sco/stats", h.scoNodeStats)
	mux.HandleFunc("DELETE /api/sco/nodes", h.deleteSCONodes)
	mux.HandleFunc("POST /api/sco/import", h.importSCONodes)
	mux.HandleFunc("GET /api/sco/artifacts", h.listSupportedArtifacts)

	// Chat session routes
	mux.HandleFunc("POST /api/chat/sessions", h.createSession)
	mux.HandleFunc("GET /api/chat/sessions", h.listSessions)
	mux.HandleFunc("GET /api/chat/sessions/{id}", h.getSession)
	mux.HandleFunc("DELETE /api/chat/sessions/{id}", h.deleteSession)
	mux.HandleFunc("POST /api/chat/sessions/{id}/messages", h.sendMessage)
	mux.HandleFunc("POST /api/chat/sessions/{id}/cancel", h.cancelSession)
	mux.HandleFunc("POST /api/chat/sessions/{id}/upload", h.uploadFile)
	mux.HandleFunc("GET /api/chat/sessions/{id}/messages", h.listMessages)
	mux.HandleFunc("GET /api/chat/sessions/{id}/commands", h.sessionCommands)
	mux.HandleFunc("GET /api/chat/sessions/{id}/events", h.sessionEvents)

	if agents != nil {
		mux.HandleFunc("/api/agents/{id}/terminal/ws", func(w http.ResponseWriter, r *http.Request) {
			agents.HandleTerminalWS(r.PathValue("id"), w, r)
		})
		mux.HandleFunc("/api/agent/ws", agents.HandleWS)
	}

	if ioaHandler != nil {
		mux.Handle("/ioa/", http.StripPrefix("/ioa", ioaHandler))
	}

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	registerLocalAgentRoutes(mux, local)

	if static != nil {
		mux.Handle("/", static)
	}

	return &Handler{handler: AccessKeyAuth(accessKey)(mux)}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	h.handler.ServeHTTP(w, r)
}

type handlerImpl struct {
	service   *Service
	agents    *AgentPool
	ioa       IOAConsoleReader
	accessKey string
}

func (h *handlerImpl) serviceStatus(w http.ResponseWriter, r *http.Request) {
	status := h.service.Status()
	if h.agents != nil {
		status.Agents = h.agents.Count()
	}
	if h.accessKey != "" {
		host := r.Host
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
			scheme = fwd
		}
		status.IOAURL = scheme + "://" + h.accessKey + "@" + host + "/ioa"
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *handlerImpl) listAgents(w http.ResponseWriter, r *http.Request) {
	if h.agents == nil {
		writeJSON(w, http.StatusOK, []AgentInfo{})
		return
	}
	writeJSON(w, http.StatusOK, h.agents.List())
}

func (h *handlerImpl) getConfig(w http.ResponseWriter, r *http.Request) {
	cs, err := h.service.GetConfigStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

func (h *handlerImpl) saveConfig(w http.ResponseWriter, r *http.Request) {
	var req webproto.DistributeConfig
	if err := decodeJSON(r.Body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cs, err := h.service.SaveConfig(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

func (h *handlerImpl) activateLLMProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cs, err := h.service.ActivateLLMProfile(r.Context(), req.ID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

func (h *handlerImpl) getDistributeConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.service.GetDistributeConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (h *handlerImpl) testLLM(w http.ResponseWriter, r *http.Request) {
	var req probe.LLMProbeRequest
	if !decodeBody(w, r, &req) {
		return
	}
	result, err := h.service.TestLLM(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handlerImpl) listLLMModels(w http.ResponseWriter, r *http.Request) {
	var req probe.LLMProbeRequest
	if !decodeBody(w, r, &req) {
		return
	}
	result, err := h.service.ListLLMModels(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handlerImpl) testConn(w http.ResponseWriter, r *http.Request) {
	var cfg webproto.DistributeConfig
	if !decodeOptionalBody(w, r, &cfg) {
		return
	}
	result, err := h.service.TestConn(r.Context(), r.PathValue("section"), cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handlerImpl) createScan(w http.ResponseWriter, r *http.Request) {
	var req ScanRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	job, err := h.service.SubmitScan(r.Context(), req.Target, req.Mode, req.Verify, req.Sniper, req.Deep)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (h *handlerImpl) listScans(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.service.ListScans(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if jobs == nil {
		jobs = []*ScanJob{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (h *handlerImpl) getScan(w http.ResponseWriter, r *http.Request) {
	job, err := h.service.GetScan(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "scan not found")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *handlerImpl) cancelScan(w http.ResponseWriter, r *http.Request) {
	if err := h.service.CancelScan(r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "canceled"})
}

func (h *handlerImpl) scanEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.service.GetScan(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "scan not found")
		return
	}
	ServeSSE(w, r, h.service.Hub(), id, "complete", "error")
}

func (h *handlerImpl) scanReport(w http.ResponseWriter, r *http.Request) {
	report, err := h.service.GetReport(r.Context(), r.PathValue("id"), r.URL.Query().Get("lang"))
	if err != nil {
		writeError(w, http.StatusNotFound, "scan not found")
		return
	}
	if report == "" {
		writeError(w, http.StatusNotFound, "report not ready")
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, report) //nolint:gosec // Content-Type is text/markdown, not HTML
}

// --- Chat session handlers ---

func (h *handlerImpl) createSession(w http.ResponseWriter, r *http.Request) {
	var req CreateSessionRequest
	if r.ContentLength > 0 {
		_ = decodeJSON(r.Body, &req)
	}
	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	session, err := h.service.CreateSession(r.Context(), req.AgentID, req.Title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (h *handlerImpl) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := h.service.ListSessions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sessions == nil {
		sessions = []*ChatSession{}
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (h *handlerImpl) getSession(w http.ResponseWriter, r *http.Request) {
	session, err := h.service.GetSession(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (h *handlerImpl) deleteSession(w http.ResponseWriter, r *http.Request) {
	if err := h.service.DeleteSession(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *handlerImpl) sendMessage(w http.ResponseWriter, r *http.Request) {
	var req SendMessageRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	opts := webproto.GoalExt{
		EvalCriteria:    strings.TrimSpace(req.EvalCriteria),
		EvalMaxRounds:   req.EvalMaxRounds,
		PersistMaxTurns: req.PersistMaxTurns,
	}
	msg, err := h.service.HandleUserMessage(r.Context(), r.PathValue("id"), req.Content, opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, msg)
}

// sessionCommands returns the web "/" command menu for a session: hub-scope
// commands merged with the bound agent's reported agent-scope commands (skills
// included). The frontend renders its slash-command popup from this, so the menu
// always reflects what actually works instead of a hand-maintained list.
func (h *handlerImpl) sessionCommands(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.service.SessionMenu(r.PathValue("id")))
}

func (h *handlerImpl) cancelSession(w http.ResponseWriter, r *http.Request) {
	if err := h.service.CancelSession(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

const maxUploadSize = 50 << 20 // 50 MB

func (h *handlerImpl) uploadFile(w http.ResponseWriter, r *http.Request) {
	// Bound the total request body before parsing so an oversized multipart
	// upload can't exhaust memory/disk (gosec G120). Allow modest headroom over
	// maxUploadSize for the multipart envelope (boundaries and part headers).
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize+(1<<20))
	if err := r.ParseMultipartForm(maxUploadSize); err != nil { //nolint:gosec // G120: body bounded by http.MaxBytesReader above
		writeError(w, http.StatusBadRequest, "file too large or invalid multipart form")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxUploadSize))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read file")
		return
	}

	result, err := h.service.HandleFileUpload(r.Context(), r.PathValue("id"), header.Filename, data)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handlerImpl) listMessages(w http.ResponseWriter, r *http.Request) {
	msgs, err := h.service.GetMessages(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if msgs == nil {
		msgs = []*ChatMessage{}
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (h *handlerImpl) sessionEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.service.GetSession(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	events, err := h.service.GetAOPEvents(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	initial := make([]HubEvent, 0, len(events))
	for _, event := range events {
		initial = append(initial, HubEvent{Type: "aop", Data: mustJSON(event)})
	}
	ServeSSEWithInitial(w, r, h.service.Hub(), sessionTopic(id), initial, "_never")
}

// ── SCO Nodes ──

func (h *handlerImpl) listSCONodes(w http.ResponseWriter, r *http.Request) {
	nodeType := r.URL.Query().Get("type")
	scanID := r.URL.Query().Get("scan_id")
	limit := 500
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	nodes, err := h.service.store.ListSCONodesByScanID(r.Context(), scanID, nodeType, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if nodes == nil {
		nodes = []json.RawMessage{}
	}
	writeJSON(w, http.StatusOK, nodes)
}

func (h *handlerImpl) getSCONode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	node, err := h.service.store.GetSCONode(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "node not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(node)
}

func (h *handlerImpl) scoNodeStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.service.store.SCONodeStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h *handlerImpl) deleteSCONodes(w http.ResponseWriter, r *http.Request) {
	scanID := r.URL.Query().Get("scan_id")
	if scanID == "" {
		writeError(w, http.StatusBadRequest, "scan_id required")
		return
	}
	if err := h.service.store.DeleteSCONodesByScan(r.Context(), scanID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func decodeJSON(body io.ReadCloser, v interface{}) error {
	defer body.Close()
	return json.NewDecoder(body).Decode(v)
}

// decodeBody decodes the JSON request body into v, writing a 400 on failure.
// Returns false when the caller should return early.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := decodeJSON(r.Body, v); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

// decodeOptionalBody decodes the request body only when one is present. An absent
// body is fine (returns true); a present-but-invalid body writes a 400 and
// returns false so the caller returns early.
func decodeOptionalBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.ContentLength == 0 {
		return true
	}
	return decodeBody(w, r, v)
}
