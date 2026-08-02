package web

import (
	"encoding/json"
	"net/http"
)

type Handler struct{ handler http.Handler }

func NewHandler(service Service, ioaHandler http.Handler, static http.Handler) *Handler {
	auth := service.Auth()
	mux := http.NewServeMux()
	auth.RegisterRoutes(mux)
	RegisterConnectServices(mux, service)
	if service != nil {
		if handler := service.ApplicationWebSocketHandler(); handler != nil {
			mux.Handle(ApplicationWebSocketPath, handler)
		}
		if handler := service.NodeWebSocketHandler(); handler != nil {
			mux.Handle(NodeWebSocketPath, handler)
		}
	}
	if ioaHandler != nil {
		mux.Handle("/ioa/", http.StripPrefix("/ioa", ioaHandler))
	}
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	if static != nil {
		mux.Handle("/", static)
	}
	return &Handler{handler: auth.Middleware(mux)}
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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
