//go:build full

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	webext "github.com/chainreactors/cyber/pkg/exts/web"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/pkg/web"
)

// newHeadlessHandler wires the RPC + AOP WebSocket surfaces without any UI:
// static is nil, so only Connect RPC, the two AOP WebSockets, and /health
// are served.
func newHeadlessHandler(ctx context.Context, database, token string) (*extension.Set, http.Handler, error) {
	webExtension := webext.New(webext.Config{Database: database, AccessKey: token})
	set, err := extension.New(webExtension)
	if err != nil {
		return nil, nil, err
	}
	if err := set.Load(ctx); err != nil {
		return nil, nil, errors.Join(err, set.Close(context.Background()))
	}
	handler, err := web.NewHandler(webExtension.Service().Auth(), nil, webExtension.Routes()...)
	if err != nil {
		return nil, nil, errors.Join(err, set.Close(context.Background()))
	}
	return set, handler, nil
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

	set, handler, err := newHeadlessHandler(ctx, dbPath, token)
	if err != nil {
		logger.Errorf("create server: %v", err)
		return
	}
	defer func() {
		if err := set.Close(context.Background()); err != nil {
			logger.Errorf("close server: %v", err)
		}
	}()

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Errorf("listen on %s: %v", addr, err)
		return
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
		return
	}
}
