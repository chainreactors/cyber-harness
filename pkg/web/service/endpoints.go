package service

import (
	"context"
	"net/http"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/pkg/aopws"
	web "github.com/chainreactors/cyber/pkg/web"
	"github.com/gorilla/websocket"
)

const (
	ApplicationWebSocketPath = web.ApplicationWebSocketPath
	NodeWebSocketPath        = web.NodeWebSocketPath
)

func serveEnvelopeWebSocket(upgrader websocket.Upgrader, serve func(context.Context, aop.EnvelopeStream) error, w http.ResponseWriter, r *http.Request) {
	if serve == nil {
		http.Error(w, "AOP WebSocket is unavailable", http.StatusServiceUnavailable)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	stream, err := aopws.New(r.Context(), conn, aopws.Options{Encoding: aopws.Binary, WriteTimeout: 10 * time.Second})
	if err != nil {
		return
	}
	defer stream.Close()
	_ = serve(r.Context(), stream)
}

func (s *Service) ApplicationWebSocketHandler() http.Handler {
	if s == nil || s.agents == nil {
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveEnvelopeWebSocket(s.agents.upgrader, s.ServeApplication, w, r)
	})
}

func (s *Service) NodeWebSocketHandler() http.Handler {
	if s == nil || s.agents == nil {
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveEnvelopeWebSocket(s.agents.upgrader, s.agents.ServeNode, w, r)
	})
}
