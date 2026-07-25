//go:build full

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
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

	if application.Provider == nil {
		logger.Warnf("%s", telemetry.StartupLine("skip", "llm", "AI disabled: set api_key in aiscan.yaml or env"))
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

	if application.SCOSidecar != nil {
		application.SCOSidecar.OnNodes = func(callID string, nodes []json.RawMessage) {
			scanID := callID
			if scanID == "" {
				scanID = "standalone"
			}
			_ = store.UpsertSCONodes(context.Background(), scanID, nodes)
		}
	}

	var pool *web.AgentPool
	if option.Debug {
		pool = web.NewAgentPool(service.Hub(), "*")
	} else {
		pool = web.NewAgentPool(service.Hub())
	}
	pool.SetRecordStore(store)
	pool.SetSCOStore(store)
	service.SetAgentPool(pool)

	staticSub, err := fs.Sub(webstatic.FS, "static")
	if err != nil {
		return fmt.Errorf("load static assets: %s", err)
	}

	accessKey := opts.Token
	if accessKey == "" {
		accessKey = protocols.NewToken()
	}
	ioaSvc := ioaserver.NewService(ioaserver.NewMemoryStore(), accessKey)
	ioaHandler := ioaserver.AuthMiddleware(ioaSvc)(ioaserver.NewHandler(ioaSvc))

	listener, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", opts.Addr, err)
	}
	defer listener.Close()
	listenAddr := listener.Addr().String()

	// Local agents: the hub can spawn `aiscan agent` children on its own host
	// (one-click launch/stop from the UI). Each child dials the hub's loopback
	// web + IOA endpoints — the IOA access key is embedded into the IOA URL — and
	// registers in the pool like any node. The hub holds the only handle to them,
	// so they are all killed on shutdown.
	localAgents := web.NewLocalAgents(hubLocalURL(listenAddr), accessKey, configFile, pool)
	go func() {
		<-ctx.Done()
		localAgents.StopAll()
	}()

	handler := web.NewHandler(service, pool, localAgents, ioaHandler, newSPAFileServer(staticSub), accessKey, ioaSvc)

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

	logger.Infof("aiscan server listening on http://%s", listenAddr)
	logger.Infof("  web access token: %s", accessKey)
	logger.Infof("  agent connect: aiscan agent --server-url http://%s@%s/ioa", accessKey, listenAddr)
	if localAgent, err := localAgents.Launch(ctx); err != nil {
		logger.Warnf("auto-start local agent: %s", err)
	} else {
		logger.Infof("auto-started local agent name=%s pid=%d", localAgent.Name, localAgent.PID)
	}
	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func newSPAFileServer(fsys fs.FS) http.HandlerFunc {
	indexBytes, _ := fs.ReadFile(fsys, "index.html")
	fileServer := http.FileServer(http.FS(fsys))
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" {
			if f, err := fsys.Open(name); err == nil {
				f.Close()
				// Vite fingerprints every asset (index-<hash>.js), so a given
				// filename's bytes never change — cache it forever. A rebuild
				// mints new filenames, so this never serves stale content.
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// Serve index.html for SPA routes. Never cache it: it is the one
		// unfingerprinted document and points at the current asset hashes.
		if len(indexBytes) > 0 {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(indexBytes)
			return
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
	appCfg.SkipEngines = true
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
	dc := parseDistributeConfig(data)
	return p, true, dc, nil
}

// parseDistributeConfig decodes the YAML settings file and migrates a legacy
// flat llm section into the provider profile list — the only place the flat
// representation is still accepted.
func parseDistributeConfig(data []byte) webproto.DistributeConfig {
	var dc webproto.DistributeConfig
	_ = yaml.Unmarshal(data, &dc)
	var legacy struct {
		LLM webproto.LLMProviderConfig `yaml:"llm"`
	}
	_ = yaml.Unmarshal(data, &legacy)
	webproto.MigrateLLMConfig(&dc.LLM, legacy.LLM)
	return dc
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
			current = parseDistributeConfig(data)
		}
	}

	// Preserve existing secrets when incoming value is empty.
	preserveLLMProfileSecrets(&incoming.LLM, current.LLM)
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

func preserveLLMProfileSecrets(incoming *webproto.LLMConfig, existing webproto.LLMConfig) {
	byID := make(map[string]webproto.LLMProviderConfig, len(existing.Providers))
	for _, profile := range existing.Providers {
		if profile.ID != "" {
			byID[profile.ID] = profile
		}
	}
	for i := range incoming.Providers {
		if strings.TrimSpace(incoming.Providers[i].APIKey) != "" {
			continue
		}
		if current, ok := byID[incoming.Providers[i].ID]; ok {
			incoming.Providers[i].APIKey = current.APIKey
			continue
		}
		if i < len(existing.Providers) {
			incoming.Providers[i].APIKey = existing.Providers[i].APIKey
		}
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

// ---------------------------------------------------------------------------
// Listen-address helpers
// ---------------------------------------------------------------------------

// hubLocalURL derives the loopback URL a local agent child should dial from the
// server listen address. A wildcard/empty host becomes 127.0.0.1; an
// unparseable address yields "".
func hubLocalURL(addr string) string {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil || port == "" {
		return ""
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}
