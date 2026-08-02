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

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	node "github.com/chainreactors/aiscan/pkg/node"
	"github.com/chainreactors/aiscan/pkg/runner"
	"github.com/chainreactors/aiscan/pkg/web"
)

// newHeadlessHandler wires the RPC + AOP WebSocket surfaces without any UI:
// static is nil, so only Connect RPC, the two AOP WebSockets, and /health
// are served.
func newHeadlessHandler(store *web.SQLiteStore, app *runner.App, token string) (*web.Service, *web.AgentPool, http.Handler) {
	service := web.NewService(web.ServiceConfig{Store: store, App: app})
	pool := web.NewAgentPool(service.Hub())
	pool.SetSCOStore(store)
	service.SetAgentPool(pool)
	return service, pool, web.NewHandler(service, nil, nil, token)
}

// acp server: aiscan headless node — no UI, RPC + AOP WebSocket only, with an
// embedded loopback agent so clients can chat immediately.
//
//	go run ./examples/acp/server --addr 127.0.0.1:8080
func main() {
	var (
		addr    string
		token   string
		dbPath  string
		noAgent bool
	)
	flag.StringVar(&addr, "addr", "127.0.0.1:8080", "listen address")
	flag.StringVar(&token, "token", "", "access token (default: generated)")
	flag.StringVar(&dbPath, "db", "acp-headless.db", "SQLite database path")
	flag.BoolVar(&noAgent, "no-agent", false, "do not embed a loopback agent")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	logger := telemetry.GlobalLogger(telemetry.LogConfig{Output: os.Stderr})

	if token == "" {
		token = fmt.Sprintf("acp-%d", time.Now().UnixNano())
	}

	store, err := web.NewSQLiteStore(dbPath)
	if err != nil {
		logger.Errorf("open database: %v", err)
		os.Exit(1)
	}
	defer store.Close()

	option := &cfg.Option{}
	if _, err := runner.ResolveRuntimeConfig(option); err != nil {
		logger.Errorf("load config: %v", err)
		os.Exit(1)
	}
	appConfig := runner.AppConfig(option, runner.RuntimeFeatures{
		ProviderEnabled: true, ProviderOptional: true, ToolsEnabled: true, AIEnabled: true,
	}, logger)
	appConfig.SkipEngines = true
	appConfig.Scanner.VerifyMode = "off"
	app, err := runner.NewApp(ctx, appConfig)
	if err != nil {
		logger.Errorf("init app: %v", err)
		os.Exit(1)
	}
	defer app.Close()

	service, _, handler := newHeadlessHandler(store, app, token)
	defer service.Close()

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

	if !noAgent {
		agentOption := *option
		agentOption.ServerURL = "http://" + token + "@" + listenAddr
		if agentOption.IOANodeName == "" {
			agentOption.IOANodeName = "local"
		}
		go func() {
			if err := node.RunWebSocket(ctx, &agentOption, logger); err != nil && ctx.Err() == nil {
				logger.Warnf("embedded agent stopped: %v", err)
			}
		}()
	}

	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		logger.Errorf("serve: %v", err)
		os.Exit(1)
	}
}
