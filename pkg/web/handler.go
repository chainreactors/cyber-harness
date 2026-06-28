package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

type Handler struct {
	mux *http.ServeMux
}

func NewHandler(service *Service, agents *AgentPool, ioaHandler http.Handler, static http.Handler) *Handler {
	mux := http.NewServeMux()

	h := &handlerImpl{service: service, agents: agents}

	mux.HandleFunc("POST /api/scans", h.createScan)
	mux.HandleFunc("GET /api/scans", h.listScans)
	mux.HandleFunc("GET /api/scans/{id}", h.getScan)
	mux.HandleFunc("DELETE /api/scans/{id}", h.cancelScan)
	mux.HandleFunc("GET /api/scans/{id}/events", h.scanEvents)
	mux.HandleFunc("GET /api/scans/{id}/report", h.scanReport)
	mux.HandleFunc("GET /api/status", h.serviceStatus)
	mux.HandleFunc("GET /api/config", h.getConfig)
	mux.HandleFunc("PUT /api/config", h.saveConfig)
	mux.HandleFunc("GET /api/config/distribute", h.getDistributeConfig)
	mux.HandleFunc("GET /api/agents", h.listAgents)

	// Chat session routes
	mux.HandleFunc("POST /api/chat/sessions", h.createSession)
	mux.HandleFunc("GET /api/chat/sessions", h.listSessions)
	mux.HandleFunc("GET /api/chat/sessions/{id}", h.getSession)
	mux.HandleFunc("DELETE /api/chat/sessions/{id}", h.deleteSession)
	mux.HandleFunc("POST /api/chat/sessions/{id}/messages", h.sendMessage)
	mux.HandleFunc("POST /api/chat/sessions/{id}/cancel", h.cancelSession)
	mux.HandleFunc("POST /api/chat/sessions/{id}/upload", h.uploadFile)
	mux.HandleFunc("GET /api/chat/sessions/{id}/messages", h.listMessages)
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

	if static != nil {
		mux.Handle("/", static)
	}

	return &Handler{mux: mux}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	h.mux.ServeHTTP(w, r)
}

type handlerImpl struct {
	service *Service
	agents  *AgentPool
}

func (h *handlerImpl) serviceStatus(w http.ResponseWriter, r *http.Request) {
	status := h.service.Status()
	if h.agents != nil {
		status.Agents = h.agents.Count()
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

func (h *handlerImpl) getDistributeConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.service.GetDistributeConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (h *handlerImpl) createScan(w http.ResponseWriter, r *http.Request) {
	var req ScanRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	verify, sniper, deep := req.AnalysisOptions()
	job, err := h.service.SubmitScan(r.Context(), req.Target, req.Mode, verify, sniper, deep)
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
	report, err := h.service.GetReport(r.Context(), r.PathValue("id"))
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
	msg, err := h.service.HandleUserMessage(r.Context(), r.PathValue("id"), req.Content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, msg)
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
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
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
	ServeSSE(w, r, h.service.Hub(), sessionTopic(id), "_never")
}

func pathSegments(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
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
