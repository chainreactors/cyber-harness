package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/chainreactors/aiscan/agent/probe"
	config "github.com/chainreactors/aiscan/core/config"
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
	registerAuthRoutes(mux, accessKey)
	connectHandler := NewConnectHandler(accessKey, service)
	mux.Handle("/aop.ChatService/", connectHandler)
	mux.Handle("/aiscan.chat.SessionService/", connectHandler)
	mux.Handle("/aiscan.scan.ScanService/", connectHandler)
	// Retired REST/SSE protocol roots must not fall through to the SPA and
	// masquerade as successful HTML responses.
	legacyNotFound := func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }
	mux.HandleFunc("/api/chat", legacyNotFound)
	mux.HandleFunc("/api/chat/", legacyNotFound)
	mux.HandleFunc("/api/scans", legacyNotFound)
	mux.HandleFunc("/api/scans/", legacyNotFound)

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
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Connect-Protocol-Version, Connect-Timeout-Ms, Connect-Content-Encoding, Connect-Accept-Encoding, Grpc-Timeout, Grpc-Encoding, Grpc-Accept-Encoding, X-Grpc-Web, X-User-Agent")
	w.Header().Set("Access-Control-Expose-Headers", "Connect-Content-Encoding, Grpc-Status, Grpc-Message, Grpc-Status-Details-Bin")
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
	var req config.DistributeConfig
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
	var cfg config.DistributeConfig
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
