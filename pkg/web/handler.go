package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type Handler struct{ handler http.Handler }

type Route struct {
	Source  string
	Pattern string
	Handler http.Handler
}

// NewHandler freezes profile-selected routes into one private ServeMux.
func NewHandler(auth Auth, static http.Handler, routes ...Route) (handler *Handler, err error) {
	if auth == nil {
		return nil, fmt.Errorf("HTTP authentication policy is required")
	}
	source := "auth"
	defer func() {
		if value := recover(); value != nil {
			handler = nil
			err = fmt.Errorf("register HTTP route from %s: %v", source, value)
		}
	}()
	mux := http.NewServeMux()
	auth.RegisterRoutes(mux)
	selected := []Route{
		{Source: "web", Pattern: "GET /health", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})},
		{Source: "web", Pattern: "/api/", Handler: http.NotFoundHandler()},
	}
	if static != nil {
		selected = append(selected, Route{Source: "web.static", Pattern: "/", Handler: static})
	}
	selected = append(selected, routes...)
	owners := map[string]string{}
	for _, route := range selected {
		source = route.Source
		if strings.TrimSpace(source) == "" || route.Handler == nil {
			return nil, fmt.Errorf("HTTP route %q requires source and handler", route.Pattern)
		}
		if previous, exists := owners[route.Pattern]; exists {
			return nil, fmt.Errorf("duplicate HTTP route %q (sources %s and %s)", route.Pattern, previous, source)
		}
		mux.Handle(route.Pattern, route.Handler)
		owners[route.Pattern] = source
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
