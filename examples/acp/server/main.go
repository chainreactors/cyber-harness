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

	aop "github.com/chainreactors/aiscan/aop"
	operationpb "github.com/chainreactors/aiscan/aop/operation"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	node "github.com/chainreactors/aiscan/pkg/node"
	profile "github.com/chainreactors/aiscan/pkg/profile/aiscan"
	"github.com/chainreactors/aiscan/pkg/runner"
	"github.com/chainreactors/aiscan/pkg/web"
	webservice "github.com/chainreactors/aiscan/pkg/web/service"
)

// newHeadlessHandler wires the RPC + AOP WebSocket surfaces without any UI:
// static is nil, so only Connect RPC, the two AOP WebSockets, and /health
// are served.
func newHeadlessHandler(store *webservice.SQLiteStore, product *profile.Profile, ingestor webservice.ArtifactIngestor, token string) (*webservice.Service, *webservice.AgentPool, http.Handler) {
	service := webservice.NewService(webservice.ServiceConfig{Store: store, Profile: product, Artifacts: ingestor, AccessKey: token})
	var app *apppkg.App
	if product != nil {
		app, _ = product.App()
	}
	pool := webservice.NewAgentPool(service.Hub())
	pool.SetArtifactIngestor(ingestor)
	service.SetAgentPool(pool)
	if app != nil && ingestor != nil {
		app.SubscribeEvents(func(event *aop.Event) {
			if event == nil || event.GetExtension() == nil {
				return
			}
			artifact := new(toolpb.Artifact)
			if event.GetExtension().MessageIs(artifact) && event.GetExtension().UnmarshalTo(artifact) == nil {
				operationID := ""
				ref := new(operationpb.Ref)
				if found, err := aop.FindTypedExtension(event, ref); err == nil && found {
					operationID = ref.GetCallId()
				}
				_, _, _ = ingestor.NormalizeArtifact(context.Background(), operationID, artifact.GetTool(), artifact.GetData())
			}
		})
	}
	return service, pool, web.NewHandler(service, nil, nil)
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

	option := &cfg.Option{}
	if _, err := runner.ResolveRuntimeConfig(option); err != nil {
		logger.Errorf("load config: %v", err)
		os.Exit(1)
	}
	appConfig := apppkg.AppConfig(option, apppkg.RuntimeFeatures{
		ProviderEnabled: true, ProviderOptional: true, ToolsEnabled: true, AIEnabled: true,
	}, logger)
	appConfig.SkipEngines = true
	appConfig.Scanner.VerifyMode = "off"
	product, err := profile.New(profile.Config{Option: option, Application: appConfig, Logger: logger})
	if err != nil {
		logger.Errorf("construct profile: %v", err)
		os.Exit(1)
	}
	if err := product.Load(ctx); err != nil {
		logger.Errorf("load profile: %v", err)
		os.Exit(1)
	}
	_, err = product.App()
	if err != nil {
		logger.Errorf("get application: %v", err)
		os.Exit(1)
	}

	service, _, handler := newHeadlessHandler(store, product, ingestor, token)
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
