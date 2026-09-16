//go:build full

package main

import (
	"context"
	"errors"
	"fmt"
	webext "github.com/chainreactors/cyber/pkg/exts/web"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	cstxext "github.com/chainreactors/cyber/pkg/exts/cstx"
	serverext "github.com/chainreactors/cyber/pkg/exts/ioa/server"
	node "github.com/chainreactors/cyber/pkg/node"
	profile "github.com/chainreactors/cyber/pkg/profile"
	"github.com/chainreactors/cyber/pkg/runner"
	types "github.com/chainreactors/cyber/pkg/types"
	"github.com/chainreactors/cyber/pkg/web"
	managementapi "github.com/chainreactors/cyber/pkg/web/api"
	webservice "github.com/chainreactors/cyber/pkg/web/service"
	ioaservice "github.com/chainreactors/cyber/tools/ioa/server"
	webstatic "github.com/chainreactors/cyber/web"
	"github.com/chainreactors/ioa/protocols"
)

func init() {
	webServeFunc = runWeb
}

func runWeb(ctx context.Context, option, explicitOption *cfg.Option, opts webCommand, logger telemetry.Logger) (resultErr error) {
	store, err := webservice.NewSQLiteStore(opts.DB)
	if err != nil {
		return fmt.Errorf("open database: %s", err)
	}
	defer store.Close()
	artifactExt, err := cstxext.New(store)
	if err != nil {
		return fmt.Errorf("init artifact normalization: %w", err)
	}
	artifactSet, err := extension.New(artifactExt)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, artifactSet.Close(context.Background())) }()
	if err := artifactSet.Load(ctx); err != nil {
		return err
	}
	ingestor := artifactExt.Importer()

	// The initial app must use the fully resolved option, including values loaded
	// from the config file and environment. explicitOption is only the seed for
	// later staged reloads, where the candidate config is resolved independently.
	product, err := initWebProfile(ctx, option, logger, ingestor)
	if err != nil {
		if product != nil {
			err = errors.Join(err, product.Close(context.Background()))
		}
		return fmt.Errorf("init aiscan: %w", err)
	}
	defer func() {
		if product != nil {
			resultErr = errors.Join(resultErr, product.Close(context.Background()))
		}
	}()
	application, err := product.App()
	if err != nil {
		return err
	}

	if provider, _ := application.ProviderState(); provider == nil {
		logger.Warnf("%s", telemetry.StartupLine("skip", "llm", "AI disabled: set api_key in cyber.yaml or env"))
	}

	configFile := option.ConfigFile
	accessKey := opts.Token
	if accessKey == "" {
		accessKey = protocols.NewToken()
	}
	service := webservice.NewService(webservice.ServiceConfig{
		ConfigAPI:   productConfigAPI(),
		Store:       store,
		Profile:     product,
		Artifacts:   ingestor,
		AccessKey:   accessKey,
		ConfigStore: &webConfigStore{explicit: configFile},
		BuildProfile: func(ctx context.Context, prepared *webservice.PreparedConfig) (profile.Application, error) {
			candidateOption := cfg.Option{}
			if explicitOption != nil {
				candidateOption = *explicitOption
			}
			candidateOption.ConfigFile = prepared.RuntimePath
			candidateOption.Sections = productSections(false)
			if _, err := runner.ResolveRuntimeConfigCandidate(&candidateOption); err != nil {
				return nil, err
			}
			// The candidate app runs exactly the proto config being committed —
			// no second parse of the staged YAML through cfg.Option.
			appCfg := applicationConfigFromDistribute(prepared.Config, profile.ProviderOptional, logger)
			appCfg = mergeApplicationOptionExtras(appCfg, &candidateOption)
			candidateProfile, err := initWebProfileFromConfig(ctx, &candidateOption, appCfg, ingestor)
			if err != nil {
				return candidateProfile, err
			}
			return candidateProfile, nil
		},
		MaxConcurrent: opts.MaxScans,
		ScanTimeout:   time.Duration(opts.ScanTimeout) * time.Second,
	})
	product = nil // Service now owns the initial profile and all replacements.
	defer func() { resultErr = errors.Join(resultErr, service.Close(context.Background())) }()

	var pool *webservice.AgentPool
	if option.Debug {
		pool = webservice.NewAgentPool(service.Hub(), ingestor, "*")
	} else {
		pool = webservice.NewAgentPool(service.Hub(), ingestor)
	}
	service.SetAgentPool(pool)

	staticSub, err := fs.Sub(webstatic.FS, "static")
	if err != nil {
		return fmt.Errorf("load static assets: %s", err)
	}

	routes := webext.New(service)
	ioaExtension := serverext.NewBrowser(ioaservice.Config{AccessKey: accessKey}, serverext.BrowserOptions{
		Authenticate: service.Auth().Authenticate,
		AuthEnabled:  service.Auth().Enabled(),
	})
	webSet, err := extension.New(routes, ioaExtension)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, webSet.Close(context.Background())) }()
	if err := webSet.Load(ctx); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", opts.Addr, err)
	}
	defer listener.Close()
	listenAddr := listener.Addr().String()

	httpHandler, err := web.NewHandler(service.Auth(), newSPAFileServer(staticSub), routes.Routes()...)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:    opts.Addr,
		Handler: httpHandler,
	}

	logger.Infof("aiscan server listening on http://%s", listenAddr)
	logger.Infof("  web access token: %s", accessKey)
	logger.Infof("  agent connect: aiscan agent --server-url http://%s@%s --node-name <name>", accessKey, listenAddr)
	embeddedCtx, stopEmbedded := context.WithCancel(ctx)
	defer stopEmbedded()
	embeddedDone := make(chan struct{})
	if !opts.NoAgent {
		// The hub's own agent comes online exactly like any node: an
		// `aiscan agent` dialed into this server over loopback WebSocket,
		// just in-process. The pool never sees a special "local" kind.
		agentOption, err := embeddedAgentOption(option, accessKey, listenAddr)
		if err != nil {
			return err
		}
		telemetry.SafeGo("embedded-agent", func() {
			defer close(embeddedDone)
			if err := node.RunWebSocket(embeddedCtx, cyberProfileFactory, &agentOption, logger); err != nil && ctx.Err() == nil {
				logger.Warnf("embedded agent stopped: %s", err)
			}
		})
	} else {
		close(embeddedDone)
	}
	return serveManagedHTTP(ctx, srv, listener, func(closeCtx context.Context) error {
		stopEmbedded()
		select {
		case <-embeddedDone:
		case <-closeCtx.Done():
			return closeCtx.Err()
		}
		return webSet.Close(closeCtx)
	})
}

func embeddedAgentOption(base *cfg.Option, accessKey, listenAddr string) (cfg.Option, error) {
	var option cfg.Option
	if base != nil {
		option = *base
	}
	if err := applyProductIdentity(&option); err != nil {
		return cfg.Option{}, err
	}
	serverURL := &url.URL{Scheme: "http", Host: listenAddr}
	serverURL.User = url.User(accessKey)
	option.ServerURL = serverURL.String()
	if option.NodeID == "" && option.NodeName == "" {
		option.NodeName = "local"
	}
	if err := cfg.ResolveAgentServerURLs(&option); err != nil {
		return cfg.Option{}, fmt.Errorf("configure embedded agent: %w", err)
	}
	return option, nil
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

func initWebProfile(ctx context.Context, baseOption *cfg.Option, logger telemetry.Logger, artifacts managementapi.ArtifactImporter) (*cyberProfile, error) {
	option := cfg.Option{}
	if baseOption != nil {
		option = *baseOption
	}
	appCfg := applicationConfigFromOption(&option, profile.ProviderOptional, logger)
	return initWebProfileFromConfig(ctx, &option, appCfg, artifacts)
}

func initWebProfileFromConfig(ctx context.Context, option *cfg.Option, appCfg applicationConfig, artifacts managementapi.ArtifactImporter) (*cyberProfile, error) {
	appCfg.SkipEngines = true

	profileConfig, err := profileConfigFromOption(option, profile.ProviderDisabled, nil, appCfg.Logger)
	if err != nil {
		return nil, err
	}
	profileConfig.Application = appCfg
	profileConfig.IOA = nil
	profileConfig.Artifacts = artifacts
	product, err := newCyberProfile(profileConfig)
	if err != nil {
		return nil, err
	}
	if err := product.Load(ctx); err != nil {
		return product, err
	}
	return product, nil
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
	s.mu.Lock()
	defer s.mu.Unlock()
	p, loaded := s.resolveConfigPath()
	if !loaded {
		return p, false, &types.DistributeConfig{}, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return p, false, nil, err
	}
	dc, err := parseProductConfig(data)
	return p, true, dc, err
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

func (s *webConfigStore) PrepareDistributeConfig(ctx context.Context, incoming *types.DistributeConfig) (*webservice.PreparedConfig, error) {
	var err error
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
		current, err = parseProductConfig(data)
		if err != nil {
			return nil, err
		}
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
	if incoming.Node == nil {
		incoming.Node = current.GetNode()
	}
	preserveLLMProfileSecrets(incoming.Llm, current.GetLlm())
	incoming.Cyberhub = preserveConfigSection(incoming.Cyberhub, current.GetCyberhub(), func(c *types.CyberhubConfig) { preserveSecret(&c.Key, current.GetCyberhub().GetKey()) })
	incoming.Recon = preserveConfigSection(incoming.Recon, current.GetRecon(), func(c *types.ReconConfig) {
		preserveSecret(&c.FofaKey, current.GetRecon().GetFofaKey())
		preserveSecret(&c.HunterApiKey, current.GetRecon().GetHunterApiKey())
	})
	incoming.Search = preserveConfigSection(incoming.Search, current.GetSearch(), func(c *types.SearchConfig) { preserveSecret(&c.TavilyKeys, current.GetSearch().GetTavilyKeys()) })
	if err := normalizeProductConfig(incoming); err != nil {
		return nil, err
	}
	sections := productSections(false)
	nextValues, currentValues := cfg.ValuesFromProto(incoming.Extensions), cfg.ValuesFromProto(current.Extensions)
	preserveProductURLCredentials(nextValues, currentValues)
	incoming.Extensions, err = cfg.ValuesToProto(sections.Preserve(nextValues, currentValues))
	if err != nil {
		return nil, err
	}
	if err = validateProductConfig(incoming); err != nil {
		return nil, err
	}

	next, err := marshalProductConfig(incoming)
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
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(p)+".tmp-*.yaml")
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
	return &webservice.PreparedConfig{
		Config: incoming, RuntimePath: tmpPath, TargetPath: p,
	}, nil
}

func (s *webConfigStore) CommitDistributeConfig(ctx context.Context, prepared *webservice.PreparedConfig) error {
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

func (s *webConfigStore) DiscardDistributeConfig(prepared *webservice.PreparedConfig) {
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
	return "cyber.yaml", false
}

func findWebConfigFile(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if _, err := os.Stat("cyber.yaml"); err == nil {
		return "cyber.yaml"
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "cyber.yaml")
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
