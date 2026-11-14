// Package host serves the Web management plane independently of a product
// profile. Products contribute profiles, routes and optional execution nodes.
package host

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strings"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	webext "github.com/chainreactors/cyber/exts/web"
	"github.com/chainreactors/cyber/pkg/web"
)

type Config struct {
	Addr       string
	Static     fs.FS
	Management webext.Config
	Extensions []extension.Extension
	Logger     telemetry.Logger
	// StartNode is optional. It runs against the bound address, under the host
	// lifetime, and must return after cancellation. A standalone Hub omits it.
	StartNode func(context.Context, string) error
	// Ready is called once routes are active and the listener has been bound.
	Ready func(string)
}

func Serve(ctx context.Context, config Config) (resultErr error) {
	logger := config.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	routes := webext.New(config.Management)
	values := append([]extension.Extension{routes}, config.Extensions...)
	set, err := extension.New(values...)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, set.Close(context.Background())) }()
	if err := set.Load(ctx); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", config.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", config.Addr, err)
	}
	defer listener.Close()
	var static http.Handler
	if config.Static != nil {
		static = SPAFileServer(config.Static)
	}
	handler, err := web.NewHandler(routes.Service().Auth(), static, routes.Routes()...)
	if err != nil {
		return err
	}
	addr := listener.Addr().String()
	logger.Infof("cyber-harness listening on http://%s", addr)
	logger.Infof("  web access token: %s", config.Management.AccessKey)
	nodeCtx, stopNode := context.WithCancel(ctx)
	defer stopNode()
	nodeDone := make(chan struct{})
	if config.StartNode != nil {
		telemetry.SafeGo("web-node", func() {
			defer close(nodeDone)
			if err := config.StartNode(nodeCtx, addr); err != nil && nodeCtx.Err() == nil {
				logger.Warnf("web node stopped: %s", err)
			}
		})
	} else {
		close(nodeDone)
	}
	if config.Ready != nil {
		config.Ready(addr)
	}
	return web.ServeHTTP(ctx, &http.Server{Handler: handler}, listener, func(closeCtx context.Context) error {
		stopNode()
		select {
		case <-nodeDone:
		case <-closeCtx.Done():
			return closeCtx.Err()
		}
		return set.Close(closeCtx)
	})
}

// SPAFileServer serves fingerprinted assets with immutable caching and keeps
// the entry document fresh for routes handled by the browser.
func SPAFileServer(fsys fs.FS) http.HandlerFunc {
	indexBytes, _ := fs.ReadFile(fsys, "index.html")
	files := http.FileServer(http.FS(fsys))
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" {
			if f, err := fsys.Open(name); err == nil {
				f.Close()
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		if len(indexBytes) > 0 {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(indexBytes)
			return
		}
		r = r.Clone(r.Context())
		r.URL.Path = "/"
		files.ServeHTTP(w, r)
	}
}
