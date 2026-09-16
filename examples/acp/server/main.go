//go:build full && cgo

package main

import (
	"context"
	"flag"
	"fmt"
	cstxext "github.com/chainreactors/cyber/pkg/exts/cstx"
	webext "github.com/chainreactors/cyber/pkg/exts/web"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/pkg/web"
	managementapi "github.com/chainreactors/cyber/pkg/web/api"
	webservice "github.com/chainreactors/cyber/pkg/web/service"
)

// newHeadlessHandler wires the RPC + AOP WebSocket surfaces without any UI:
// static is nil, so only Connect RPC, the two AOP WebSockets, and /health
// are served.
func newHeadlessHandler(store *webservice.SQLiteStore, ingestor managementapi.ArtifactImporter, token string) (*webservice.Service, *webservice.AgentPool, http.Handler, error) {
	service := webservice.NewService(webservice.ServiceConfig{Store: store, Artifacts: ingestor, AccessKey: token})
	pool := webservice.NewAgentPool(service.Hub(), ingestor)
	service.SetAgentPool(pool)
	routes := webext.New(service)
	routeSet, err := extension.New(routes)
	if err != nil {
		_ = service.Close(context.Background())
		return nil, nil, nil, err
	}
	if err := routeSet.Load(context.Background()); err != nil {
		_ = service.Close(context.Background())
		return nil, nil, nil, err
	}
	handler, err := web.NewHandler(service.Auth(), nil, routes.Routes()...)
	closeErr := routeSet.Close(context.Background())
	if err != nil {
		_ = service.Close(context.Background())
		return nil, nil, nil, err
	}
	if closeErr != nil {
		_ = service.Close(context.Background())
		return nil, nil, nil, closeErr
	}
	return service, pool, handler, nil
}

// acp server: Cyber headless control plane — no UI and no hidden local
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
	artifactExt, err := cstxext.New(store)
	if err != nil {
		logger.Errorf("init artifact normalization: %v", err)
		os.Exit(1)
	}
	artifactSet, err := extension.New(artifactExt)
	if err != nil {
		logger.Errorf("init artifact scope: %v", err)
		os.Exit(1)
	}
	defer func() {
		if err := artifactSet.Close(context.Background()); err != nil {
			logger.Errorf("close artifact scope: %v", err)
		}
	}()
	if err := artifactSet.Load(ctx); err != nil {
		logger.Errorf("load artifact scope: %v", err)
		os.Exit(1)
	}
	ingestor := artifactExt.Importer()

	service, _, handler, err := newHeadlessHandler(store, ingestor, token)
	if err != nil {
		logger.Errorf("create handler: %v", err)
		return
	}
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
