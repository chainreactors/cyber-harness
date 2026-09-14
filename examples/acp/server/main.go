//go:build full

package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/web"
	webservice "github.com/chainreactors/aiscan/pkg/web/service"
)

// newHeadlessHandler wires the RPC + AOP WebSocket surfaces without any UI:
// static is nil, so only Connect RPC, the two AOP WebSockets, and /health
// are served.
func newHeadlessHandler(store *webservice.SQLiteStore, ingestor webservice.ArtifactIngestor, token string) (*webservice.Service, *webservice.AgentPool, http.Handler) {
	service := webservice.NewService(webservice.ServiceConfig{Store: store, Artifacts: ingestor, AccessKey: token})
	pool := webservice.NewAgentPool(service.Hub())
	pool.SetArtifactIngestor(ingestor)
	service.SetAgentPool(pool)
	return service, pool, web.NewHandler(service, nil, nil)
}

// acp server: AIScan headless control plane — no UI and no hidden local
// application graph. Agents connect through the public AOP endpoint.
//
//	go run ./examples/acp/server --addr 127.0.0.1:8080
func main() {
	var (
		addr   string
		token  string
		dbPath string
	)
	flag.StringVar(&addr, "addr", "127.0.0.1:8080", "listen address")
	flag.StringVar(&token, "token", "", "access token (default: generated)")
	flag.StringVar(&dbPath, "db", "acp-headless.db", "SQLite database path")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	logger := telemetry.GlobalLogger(telemetry.LogConfig{Output: os.Stderr})

	if token == "" {
		token = fmt.Sprintf("acp-%d", time.Now().UnixNano())
	}

	store, err := webservice.NewSQLiteStore(dbPath)
	if err != nil {
		logger.Errorf("open database: %v", err)
		os.Exit(1)
	}
	defer store.Close()
	ingestor, err := webservice.NewArtifactIngestor(store)
	if err != nil {
		logger.Errorf("init artifact normalization: %v", err)
		os.Exit(1)
	}
	defer ingestor.Close()

	service, _, handler := newHeadlessHandler(store, ingestor, token)
	defer func() {
		if err := service.Close(context.Background()); err != nil {
			logger.Errorf("close service: %v", err)
		}
	}()

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Errorf("listen on %s: %v", addr, err)
		os.Exit(1)
	}
	defer listener.Close()
	listenAddr := listener.Addr().String()

	srv := &http.Server{Handler: handler}
	go func() {
		<-ctx.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		_ = srv.Shutdown(shutCtx)
	}()

	logger.Infof("acp headless server listening on http://%s", listenAddr)
	logger.Infof("  access token: %s", token)

	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		logger.Errorf("serve: %v", err)
		os.Exit(1)
	}
}
