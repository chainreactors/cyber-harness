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
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/runner"
	types "github.com/chainreactors/aiscan/pkg/types"
	"github.com/chainreactors/aiscan/pkg/web"
	webstatic "github.com/chainreactors/aiscan/web"
	"github.com/chainreactors/ioa/protocols"
	ioaserver "github.com/chainreactors/ioa/server"
)

func init() {
	webServeFunc = runWeb
}

func runWeb(ctx context.Context, option, explicitOption *cfg.Option, opts webCommand, logger telemetry.Logger) error {
	store, err := web.NewSQLiteStore(opts.DB)
	if err != nil {
		return fmt.Errorf("open database: %s", err)
	}
	defer store.Close()

	// The initial app must use the fully resolved option, including values loaded
	// from the config file and environment. explicitOption is only the seed for
	// later staged reloads, where the candidate config is resolved independently.
	application, err := initWebApp(ctx, option, logger)
	if err != nil {
		return fmt.Errorf("init aiscan: %s", err)
	}

	if application.Provider == nil {
		logger.Warnf("%s", telemetry.StartupLine("skip", "llm", "AI disabled: set api_key in aiscan.yaml or env"))
	}

	configFile := option.ConfigFile
	service := web.NewService(web.ServiceConfig{
		Store:       store,
		App:         application,
		ConfigStore: &webConfigStore{explicit: configFile},
		AppFactory: func(ctx context.Context, prepared *web.PreparedConfig) (*runner.App, error) {
			candidateOption := cfg.Option{}
			if explicitOption != nil {
				candidateOption = *explicitOption
			}
			candidateOption.ConfigFile = prepared.RuntimePath
			if _, err := runner.ResolveRuntimeConfigCandidate(&candidateOption); err != nil {
				return nil, err
			}
			// The candidate app runs exactly the proto config being committed —
			// no second parse of the staged YAML through cfg.Option.
			appCfg := runner.AppConfigFromDistribute(prepared.Config, runner.RuntimeFeatures{
				ProviderEnabled:  true,
				ProviderOptional: true,
				ToolsEnabled:     true,
				AIEnabled:        true,
			}, logger)
			appCfg = runner.MergeOptionExtras(appCfg, &candidateOption)
			candidate, err := initWebAppFromConfig(ctx, appCfg)
			if err != nil {
				return nil, err
			}
			wireWebApp(candidate, store)
			return candidate, nil
		},
		MaxConcurrent: opts.MaxScans,
		ScanTimeout:   time.Duration(opts.ScanTimeout) * time.Second,
	})
	defer service.Close()

	wireWebApp(application, store)

	var pool *web.AgentPool
	if option.Debug {
		pool = web.NewAgentPool(service.Hub(), "*")
	} else {
		pool = web.NewAgentPool(service.Hub())
	}
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
	ioaWebIdentity, err := ioaSvc.AuthRegister(ctx, protocols.AuthRegister{
		Name:        "aiscan.web",
		Description: "AIScan Web console",
		AccessKey:   accessKey,
		Meta:        map[string]any{"role": "web"},
	})
	if err != nil {
		return fmt.Errorf("register IOA web identity: %w", err)
	}
	ioaHandler := web.ShareWebAuthWithIOA(
		accessKey,
		ioaWebIdentity.Token,
		ioaserver.AuthMiddleware(ioaSvc)(ioaserver.NewHandler(ioaSvc)),
	)

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

	httpHandler := web.NewHandler(service, pool, localAgents, ioaHandler, newSPAFileServer(staticSub), accessKey)

	srv := &http.Server{
		Addr:    opts.Addr,
		Handler: httpHandler,
	}

	go func() {
		<-ctx.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		_ = srv.Shutdown(shutCtx)
	}()

	logger.Infof("aiscan server listening on http://%s", listenAddr)
	logger.Infof("  web access token: %s", accessKey)
	logger.Infof("  agent connect: aiscan agent --server-url http://%s@%s --node-name <name>", accessKey, listenAddr)
	if localAgent, err := localAgents.Launch(ctx); err != nil {
		logger.Warnf("auto-start local agent: %s", err)
	} else {
		logger.Infof("auto-started local agent name=%s pid=%d", localAgent.Name, localAgent.Pid)
	}
	if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func wireWebApp(application *runner.App, store *web.SQLiteStore) {
	if application == nil || store == nil || application.SCOSidecar == nil {
		return
	}
	application.SCOSidecar.OnNodes = func(callID string, nodes []json.RawMessage) {
		_ = store.UpsertSCONodes(context.Background(), callID, nodes)
	}
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
	appCfg := runner.AppConfig(&option, runner.RuntimeFeatures{
		ProviderEnabled:  true,
		ProviderOptional: true,
		ToolsEnabled:     true,
		AIEnabled:        true,
	}, logger)
	return initWebAppFromConfig(ctx, appCfg)
}

func initWebAppFromConfig(ctx context.Context, appCfg runner.ApplicationConfig) (*runner.App, error) {
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

func (s *webConfigStore) GetDistributeConfig(ctx context.Context) (string, bool, *types.DistributeConfig, error) {
	if err := ctx.Err(); err != nil {
		return "", false, nil, err
	}
	p, loaded := s.resolveConfigPath()
	if !loaded {
		return p, false, &types.DistributeConfig{}, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return p, false, nil, err
	}
	dc := parseDistributeConfig(data)
	return p, true, dc, nil
}

// parseDistributeConfig decodes the final protobuf-shaped YAML configuration.
func parseDistributeConfig(data []byte) *types.DistributeConfig {
	dc, err := cfg.LoadDistributeConfigYAML(data)
	if err != nil || dc == nil {
		dc = &types.DistributeConfig{}
	}
	if dc.Llm == nil {
		dc.Llm = &types.LLMConfig{}
	}
	cfg.NormalizeLLMConfig(dc.Llm)
	return dc
}

func (s *webConfigStore) PrepareDistributeConfig(ctx context.Context, incoming *types.DistributeConfig) (*web.PreparedConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	p, loaded := s.resolveConfigPath()
	var current *types.DistributeConfig
	if loaded {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		current = parseDistributeConfig(data)
	} else {
		current = &types.DistributeConfig{}
	}
	if incoming == nil {
		incoming = &types.DistributeConfig{}
	}
	if incoming.Llm == nil {
		incoming.Llm = &types.LLMConfig{}
	}
	cfg.NormalizeLLMConfig(incoming.Llm)

	// Preserve existing secrets when incoming value is empty.
	preserveLLMProfileSecrets(incoming.Llm, current.GetLlm())
	incoming.Cyberhub = preserveConfigSection(incoming.Cyberhub, current.GetCyberhub(), func(c *types.CyberhubConfig) { preserveSecret(&c.Key, current.GetCyberhub().GetKey()) })
	incoming.Recon = preserveConfigSection(incoming.Recon, current.GetRecon(), func(c *types.ReconConfig) {
		preserveSecret(&c.FofaKey, current.GetRecon().GetFofaKey())
		preserveSecret(&c.HunterToken, current.GetRecon().GetHunterToken())
		preserveSecret(&c.HunterApiKey, current.GetRecon().GetHunterApiKey())
	})
	incoming.Search = preserveConfigSection(incoming.Search, current.GetSearch(), func(c *types.SearchConfig) { preserveSecret(&c.TavilyKeys, current.GetSearch().GetTavilyKeys()) })
	incoming.Ioa = preserveConfigSection(incoming.Ioa, current.GetIoa(), func(c *types.IOAConfig) { preserveSecret(&c.Token, current.GetIoa().GetToken()) })

	next, err := cfg.MarshalDistributeConfigYAML(incoming)
	if err != nil {
		return nil, err
	}
	if dir := filepath.Dir(p); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	}
	dir := filepath.Dir(p)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(p)+".tmp-*")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(0600); err != nil {
		cleanup()
		return nil, err
	}
	if _, err := tmp.Write(next); err != nil {
		cleanup()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return nil, err
	}
	return &web.PreparedConfig{
		Config: incoming, RuntimePath: tmpPath, TargetPath: p,
	}, nil
}

func (s *webConfigStore) CommitDistributeConfig(ctx context.Context, prepared *web.PreparedConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if prepared == nil || prepared.RuntimePath == "" || prepared.TargetPath == "" {
		return fmt.Errorf("prepared config is incomplete")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := replaceConfigFile(prepared.RuntimePath, prepared.TargetPath); err != nil {
		return err
	}
	prepared.RuntimePath = ""
	if dir, err := os.Open(filepath.Dir(prepared.TargetPath)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func (s *webConfigStore) DiscardDistributeConfig(prepared *web.PreparedConfig) {
	if prepared == nil || prepared.RuntimePath == "" {
		return
	}
	_ = os.Remove(prepared.RuntimePath)
	prepared.RuntimePath = ""
}

func preserveSecret(incoming *string, existing string) {
	if strings.TrimSpace(*incoming) == "" {
		*incoming = existing
	}
}

// preserveConfigSection ensures section is non-nil, then applies fn to it.
// current is the on-disk value used to backfill empty secrets.
func preserveConfigSection[T any](incoming *T, current *T, fn func(*T)) *T {
	if incoming == nil {
		if current != nil {
			return current
		}
		return new(T)
	}
	fn(incoming)
	return incoming
}

func preserveLLMProfileSecrets(incoming *types.LLMConfig, existing *types.LLMConfig) {
	if incoming == nil {
		return
	}
	byID := make(map[string]*types.LLMProviderConfig)
	if existing != nil {
		for _, profile := range existing.Providers {
			if profile.Id != "" {
				byID[profile.Id] = profile
			}
		}
	}
	var existingProviders []*types.LLMProviderConfig
	if existing != nil {
		existingProviders = existing.Providers
	}
	for i, profile := range incoming.Providers {
		if profile == nil || strings.TrimSpace(profile.ApiKey) != "" {
			continue
		}
		if current, ok := byID[profile.Id]; ok {
			profile.ApiKey = current.ApiKey
			continue
		}
		if i < len(existingProviders) {
			profile.ApiKey = existingProviders[i].GetApiKey()
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
