package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type Handler struct{ handler http.Handler }

type Route struct {
	Pattern string
	Handler http.Handler
}

// NewHandler freezes profile-selected routes into one private ServeMux.
func NewHandler(auth Auth, static http.Handler, routes ...Route) (handler *Handler, err error) {
	if auth == nil {
		return nil, fmt.Errorf("HTTP authentication policy is required")
	}
	pattern := "authentication routes"
	defer func() {
		if value := recover(); value != nil {
			handler = nil
			err = fmt.Errorf("register HTTP route %s: %v", pattern, value)
		}
	}()
	mux := http.NewServeMux()
	auth.RegisterRoutes(mux)
	selected := []Route{
		{Pattern: "GET /health", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})},
		{Pattern: "/api/", Handler: http.NotFoundHandler()},
	}
	if static != nil {
		selected = append(selected, Route{Pattern: "/", Handler: static})
	}
	selected = append(selected, routes...)
	patterns := map[string]bool{}
	for _, route := range selected {
		pattern = route.Pattern
		if strings.TrimSpace(pattern) == "" || route.Handler == nil {
			return nil, fmt.Errorf("HTTP route requires pattern and handler")
		}
		if patterns[pattern] {
			return nil, fmt.Errorf("duplicate HTTP route %q", pattern)
		}
		mux.Handle(pattern, route.Handler)
		patterns[pattern] = true
	}
	return &Handler{handler: auth.Middleware(mux)}, nil
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
