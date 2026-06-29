//go:build full

package main

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/runner"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/web"
	"github.com/chainreactors/aiscan/pkg/webproto"
	webstatic "github.com/chainreactors/aiscan/web"
	"github.com/chainreactors/ioa/protocols"
	ioaserver "github.com/chainreactors/ioa/server"
	"gopkg.in/yaml.v3"
)

func init() {
	webServeFunc = runWeb
}

func runWeb(ctx context.Context, option *cfg.Option, opts webCommand, logger telemetry.Logger) error {
	store, err := web.NewSQLiteStore(opts.DB)
	if err != nil {
		return fmt.Errorf("open database: %s", err)
	}
	defer store.Close()

	application, err := initWebApp(ctx, option, logger)
	if err != nil {
		return fmt.Errorf("init aiscan: %s", err)
	}

	if application.Provider != nil {
		logger.Infof("LLM provider ready, AI features enabled")
	} else {
		logger.Warnf("no LLM provider configured, AI features disabled (set api_key in aiscan.yaml or env)")
	}

	configFile := option.ConfigFile
	appOption := *option
	service := web.NewService(web.ServiceConfig{
		Store:         store,
		App:           application,
		ConfigStore:   &webConfigStore{explicit: configFile},
		AppFactory:    func(ctx context.Context) (*runner.App, error) { return initWebApp(ctx, &appOption, logger) },
		MaxConcurrent: opts.MaxScans,
		ScanTimeout:   time.Duration(opts.ScanTimeout) * time.Second,
	})
	defer service.Close()

	var pool *web.AgentPool
	if option.Debug {
		pool = web.NewAgentPool(service.Hub(), "*")
	} else {
		pool = web.NewAgentPool(service.Hub())
	}
	pool.SetRecordStore(store)
	service.SetAgentPool(pool)

	staticSub, err := fs.Sub(webstatic.FS, "static")
	if err != nil {
		return fmt.Errorf("load static assets: %s", err)
	}

	accessKey := opts.IOAToken
	if accessKey == "" {
		accessKey = protocols.NewToken()
	}
	ioaSvc := ioaserver.NewService(ioaserver.NewMemoryStore(), accessKey)
	ioaHandler := ioaserver.AuthMiddleware(ioaSvc)(ioaserver.NewHandler(ioaSvc))

	handler := web.NewHandler(service, pool, ioaHandler, newSPAFileServer(staticSub))

	srv := &http.Server{
		Addr:    opts.Addr,
		Handler: handler,
	}

	go func() {
		<-ctx.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		_ = srv.Shutdown(shutCtx)
	}()

	logger.Infof("aiscan web server listening on http://%s", opts.Addr)
	logger.Infof("IOA server embedded at http://%s/ioa (token=%s)", opts.Addr, accessKey)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func newSPAFileServer(fsys fs.FS) http.HandlerFunc {
	fileServer := http.FileServer(http.FS(fsys))
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" {
			if f, err := fsys.Open(name); err == nil {
				f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		r = r.Clone(r.Context())
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	}
}

func initWebApp(ctx context.Context, baseOption *cfg.Option, logger telemetry.Logger) (*runner.App, error) {
	option := cfg.Option{}
	if baseOption != nil {
		option = *baseOption
	}
	cfgPath, err := cfg.ResolveRuntimeConfig(&option)
	if err != nil {
		return nil, err
	}
	if cfgPath != "" {
		logger.Infof("loaded config: %s", cfgPath)
	}

	appCfg := cfg.AppConfig(&option, cfg.RuntimeFeatures{
		ProviderEnabled:  true,
		ProviderOptional: true,
		ToolsEnabled:     true,
		AIEnabled:        true,
	}, logger)
	appCfg.Scanner.EnableAllAISkills = false
	appCfg.Scanner.VerifyMode = "off"

	app, err := runner.NewApp(ctx, appCfg)
	if err != nil {
		return nil, err
	}
	if err := app.WaitEngines(ctx); err != nil {
		app.Close()
		return nil, err
	}
	return app, nil
}

// ---------------------------------------------------------------------------
// Config file store for web UI settings page
// ---------------------------------------------------------------------------

type webConfigStore struct {
	explicit string
	mu       sync.Mutex
}

func (s *webConfigStore) GetDistributeConfig(ctx context.Context) (string, bool, webproto.DistributeConfig, error) {
	if err := ctx.Err(); err != nil {
		return "", false, webproto.DistributeConfig{}, err
	}
	p, loaded := s.resolveConfigPath()
	if !loaded {
		return p, false, webproto.DistributeConfig{}, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return p, false, webproto.DistributeConfig{}, err
	}
	var dc webproto.DistributeConfig
	_ = yaml.Unmarshal(data, &dc)
	return p, true, dc, nil
}

func (s *webConfigStore) SaveDistributeConfig(ctx context.Context, incoming webproto.DistributeConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	p, loaded := s.resolveConfigPath()
	var current webproto.DistributeConfig
	if loaded {
		if data, err := os.ReadFile(p); err == nil {
			_ = yaml.Unmarshal(data, &current)
		}
	}

	// Preserve existing secrets when incoming value is empty.
	preserveSecret(&incoming.LLM.APIKey, current.LLM.APIKey)
	preserveSecret(&incoming.Cyberhub.Key, current.Cyberhub.Key)
	preserveSecret(&incoming.Recon.FofaKey, current.Recon.FofaKey)
	preserveSecret(&incoming.Recon.HunterToken, current.Recon.HunterToken)
	preserveSecret(&incoming.Recon.HunterAPIKey, current.Recon.HunterAPIKey)
	preserveSecret(&incoming.Search.TavilyKeys, current.Search.TavilyKeys)
	preserveSecret(&incoming.IOA.Token, current.IOA.Token)

	next, _ := yaml.Marshal(&incoming)
	if dir := filepath.Dir(p); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return os.WriteFile(p, next, 0600)
}

func preserveSecret(incoming *string, existing string) {
	if strings.TrimSpace(*incoming) == "" {
		*incoming = existing
	}
}

func (s *webConfigStore) resolveConfigPath() (string, bool) {
	p := findWebConfigFile(s.explicit)
	if p != "" {
		return p, true
	}
	if s.explicit != "" {
		return s.explicit, false
	}
	return "aiscan.yaml", false
}

func findWebConfigFile(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if _, err := os.Stat("aiscan.yaml"); err == nil {
		return "aiscan.yaml"
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "aiscan.yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

