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

	application, err := initWebApp(ctx, option.ConfigFile, logger)
	if err != nil {
		return fmt.Errorf("init aiscan: %s", err)
	}

	if application.Provider != nil {
		logger.Infof("LLM provider ready, AI features enabled")
	} else {
		logger.Warnf("no LLM provider configured, AI features disabled (set api_key in aiscan.yaml or env)")
	}

	configFile := option.ConfigFile
	service := web.NewService(web.ServiceConfig{
		Store:         store,
		App:           application,
		ConfigStore:   &webConfigStore{explicit: configFile},
		AppFactory:    func(ctx context.Context) (*runner.App, error) { return initWebApp(ctx, configFile, logger) },
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

func initWebApp(ctx context.Context, configFile string, logger telemetry.Logger) (*runner.App, error) {
	option := cfg.Option{}
	if configFile != "" {
		option.ConfigFile = configFile
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
// LLM config file store for web UI settings page
// ---------------------------------------------------------------------------

type webYAMLConfig struct {
	LLM struct {
		Provider string `yaml:"provider"`
		BaseURL  string `yaml:"base_url"`
		APIKey   string `yaml:"api_key"`
		Model    string `yaml:"model"`
		Proxy    string `yaml:"proxy"`
	} `yaml:"llm"`
	Cyberhub struct {
		URL   string `yaml:"url"`
		Key   string `yaml:"key"`
		Mode  string `yaml:"mode"`
		Proxy string `yaml:"proxy"`
	} `yaml:"cyberhub"`
	Scan struct {
		Verify        string `yaml:"verify"`
		VerifyTimeout int    `yaml:"verify_timeout"`
	} `yaml:"scan"`
	Search struct {
		TavilyKeys string `yaml:"tavily_keys"`
	} `yaml:"search"`
}

type webConfigStore struct {
	explicit string
	mu       sync.Mutex
}

func (s *webConfigStore) GetLLMConfig(ctx context.Context) (web.LLMConfig, error) {
	if err := ctx.Err(); err != nil {
		return web.LLMConfig{}, err
	}
	p, loaded := s.resolveConfigPath()
	c := webYAMLConfig{}
	if loaded {
		c = loadWebYAML(p)
	}
	return web.LLMConfig{
		ConfigPath:       p,
		ConfigLoaded:     loaded,
		Provider:         c.LLM.Provider,
		BaseURL:          c.LLM.BaseURL,
		APIKeyConfigured: strings.TrimSpace(c.LLM.APIKey) != "",
		Model:            c.LLM.Model,
		Proxy:            c.LLM.Proxy,
	}, nil
}

func (s *webConfigStore) SaveLLMConfig(ctx context.Context, llmCfg web.LLMConfig) (web.LLMConfig, error) {
	if err := ctx.Err(); err != nil {
		return web.LLMConfig{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	p, loaded := s.resolveConfigPath()
	var data []byte
	if loaded {
		current, err := os.ReadFile(p)
		if err != nil {
			return web.LLMConfig{}, err
		}
		data = current
	}

	current := webYAMLConfig{}
	if len(data) > 0 {
		_ = yaml.Unmarshal(data, &current)
	}
	apiKey := strings.TrimSpace(llmCfg.APIKey)
	if apiKey == "" {
		apiKey = current.LLM.APIKey
	}

	current.LLM.Provider = strings.TrimSpace(llmCfg.Provider)
	current.LLM.BaseURL = strings.TrimSpace(llmCfg.BaseURL)
	current.LLM.APIKey = apiKey
	current.LLM.Model = strings.TrimSpace(llmCfg.Model)
	current.LLM.Proxy = strings.TrimSpace(llmCfg.Proxy)
	next, _ := yaml.Marshal(&current)
	if dir := filepath.Dir(p); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return web.LLMConfig{}, err
		}
	}
	if err := os.WriteFile(p, next, 0600); err != nil {
		return web.LLMConfig{}, err
	}
	saved := loadWebYAML(p)
	return web.LLMConfig{
		ConfigPath:       p,
		ConfigLoaded:     true,
		Provider:         saved.LLM.Provider,
		BaseURL:          saved.LLM.BaseURL,
		APIKeyConfigured: strings.TrimSpace(saved.LLM.APIKey) != "",
		Model:            saved.LLM.Model,
		Proxy:            saved.LLM.Proxy,
	}, nil
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

func loadWebYAML(path string) webYAMLConfig {
	var c webYAMLConfig
	if path == "" {
		return c
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	_ = yaml.Unmarshal(data, &c)
	return c
}
