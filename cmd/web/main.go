package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	dbPath := flag.String("db", "aiscan-web.db", "SQLite database path")
	configFile := flag.String("config", "", "Path to aiscan config.yaml")
	debug := flag.Bool("debug", false, "Enable debug logging")
	maxScans := flag.Int("max-scans", 3, "Maximum concurrent scans")
	scanTimeout := flag.Int("scan-timeout", 600, "Maximum scan runtime in seconds")
	ioaToken := flag.String("ioa-token", "", "IOA access key (auto-generated if empty)")
	flag.Parse()

	logger := telemetry.GlobalLogger(telemetry.LogConfig{
		Debug:  *debug,
		Output: os.Stderr,
		Color:  true,
	})

	store, err := web.NewSQLiteStore(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open database: %s\n", err)
		os.Exit(1)
	}
	defer store.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := initApp(ctx, *configFile, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: init aiscan: %s\n", err)
		os.Exit(1)
	}

	if application.Provider != nil {
		logger.Infof("LLM provider ready, AI features enabled")
	} else {
		logger.Warnf("no LLM provider configured, AI features disabled (set api_key in config.yaml or env)")
	}

	service := web.NewService(web.ServiceConfig{
		Store:         store,
		App:           application,
		ConfigStore:   &llmConfigFileStore{explicit: *configFile},
		AppFactory:    func(ctx context.Context) (*runner.App, error) { return initApp(ctx, *configFile, logger) },
		MaxConcurrent: *maxScans,
		ScanTimeout:   time.Duration(*scanTimeout) * time.Second,
	})
	defer service.Close()

	var pool *web.AgentPool
	if *debug {
		pool = web.NewAgentPool(service.Hub(), "*")
	} else {
		pool = web.NewAgentPool(service.Hub())
	}
	service.SetAgentPool(pool)

	staticSub, err := fs.Sub(webstatic.FS, "static")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load static assets: %s\n", err)
		os.Exit(1)
	}

	// Embedded IOA server
	accessKey := *ioaToken
	if accessKey == "" {
		accessKey = protocols.NewToken()
	}
	ioaSvc := ioaserver.NewService(ioaserver.NewMemoryStore(), accessKey)
	ioaHandler := ioaserver.AuthMiddleware(ioaSvc)(ioaserver.NewHandler(ioaSvc))

	handler := web.NewHandler(service, pool, ioaHandler, newSPAFileServer(staticSub))

	srv := &http.Server{
		Addr:    *addr,
		Handler: handler,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Infof("shutting down...")
		cancel()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		_ = srv.Shutdown(shutCtx)
	}()

	logger.Infof("aiscan web server listening on http://%s", *addr)
	logger.Infof("IOA server embedded at http://%s/ioa (token=%s)", *addr, accessKey)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
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

type yamlConfig struct {
	LLM struct {
		Provider string `yaml:"provider" config:"provider"`
		BaseURL  string `yaml:"base_url" config:"base_url"`
		APIKey   string `yaml:"api_key" config:"api_key"`
		Model    string `yaml:"model" config:"model"`
		Proxy    string `yaml:"proxy" config:"proxy"`
	} `yaml:"llm" config:"llm"`
	Cyberhub struct {
		URL   string `yaml:"url" config:"url"`
		Key   string `yaml:"key" config:"key"`
		Mode  string `yaml:"mode" config:"mode"`
		Proxy string `yaml:"proxy" config:"proxy"`
	} `yaml:"cyberhub" config:"cyberhub"`
	Scan struct {
		Verify        string `yaml:"verify" config:"verify"`
		VerifyTimeout int    `yaml:"verify_timeout" config:"verify_timeout"`
	} `yaml:"scan" config:"scan"`
	Search struct {
		TavilyKeys string `yaml:"tavily_keys" config:"tavily_keys"`
	} `yaml:"search" config:"search"`
}

type llmConfigFileStore struct {
	explicit string
	mu       sync.Mutex
}

func (s *llmConfigFileStore) GetLLMConfig(ctx context.Context) (web.LLMConfig, error) {
	if err := ctx.Err(); err != nil {
		return web.LLMConfig{}, err
	}
	path, loaded := s.resolvePath()
	cfg := yamlConfig{}
	if loaded {
		cfg = loadYAMLConfig(path)
	}
	return web.LLMConfig{
		ConfigPath:       path,
		ConfigLoaded:     loaded,
		Provider:         cfg.LLM.Provider,
		BaseURL:          cfg.LLM.BaseURL,
		APIKeyConfigured: strings.TrimSpace(cfg.LLM.APIKey) != "",
		Model:            cfg.LLM.Model,
		Proxy:            cfg.LLM.Proxy,
	}, nil
}

func (s *llmConfigFileStore) SaveLLMConfig(ctx context.Context, cfg web.LLMConfig) (web.LLMConfig, error) {
	if err := ctx.Err(); err != nil {
		return web.LLMConfig{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path, loaded := s.resolvePath()
	var data []byte
	if loaded {
		current, err := os.ReadFile(path)
		if err != nil {
			return web.LLMConfig{}, err
		}
		data = current
	}

	current := yamlConfig{}
	if len(data) > 0 {
		_ = yaml.Unmarshal(data, &current)
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		apiKey = current.LLM.APIKey
	}

	current.LLM.Provider = strings.TrimSpace(cfg.Provider)
	current.LLM.BaseURL = strings.TrimSpace(cfg.BaseURL)
	current.LLM.APIKey = apiKey
	current.LLM.Model = strings.TrimSpace(cfg.Model)
	current.LLM.Proxy = strings.TrimSpace(cfg.Proxy)
	next, _ := yaml.Marshal(&current)
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return web.LLMConfig{}, err
		}
	}
	if err := os.WriteFile(path, next, 0600); err != nil {
		return web.LLMConfig{}, err
	}
	saved := loadYAMLConfig(path)
	return web.LLMConfig{
		ConfigPath:       path,
		ConfigLoaded:     true,
		Provider:         saved.LLM.Provider,
		BaseURL:          saved.LLM.BaseURL,
		APIKeyConfigured: strings.TrimSpace(saved.LLM.APIKey) != "",
		Model:            saved.LLM.Model,
		Proxy:            saved.LLM.Proxy,
	}, nil
}

func (s *llmConfigFileStore) resolvePath() (string, bool) {
	path := findConfigFile(s.explicit)
	if path != "" {
		return path, true
	}
	if s.explicit != "" {
		return s.explicit, false
	}
	return "config.yaml", false
}


func findConfigFile(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if _, err := os.Stat("config.yaml"); err == nil {
		return "config.yaml"
	}
	if dir, err := os.UserConfigDir(); err == nil {
		p := filepath.Join(dir, "aiscan", "config.yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func loadYAMLConfig(path string) yamlConfig {
	var c yamlConfig
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

func initApp(ctx context.Context, configFile string, logger telemetry.Logger) (*runner.App, error) {
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

