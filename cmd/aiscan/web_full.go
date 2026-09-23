//go:build full

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	types "github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
	ioaserver "github.com/chainreactors/cyber/pkg/exts/ioa/server"
	webext "github.com/chainreactors/cyber/pkg/exts/web"
	node "github.com/chainreactors/cyber/pkg/node"
	profile "github.com/chainreactors/cyber/pkg/profile"

	"github.com/chainreactors/cyber/pkg/web"
	webservice "github.com/chainreactors/cyber/pkg/web/service"
	ioaservice "github.com/chainreactors/cyber/tools/ioa/server"
	webstatic "github.com/chainreactors/cyber/web"
	"github.com/chainreactors/ioa/protocols"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

func serveWeb(ctx context.Context, option, explicitOption *cfg.Option, opts webCommand, logger telemetry.Logger) (resultErr error) {
	configFile := option.ConfigFile
	accessKey := opts.Token
	if accessKey == "" {
		accessKey = protocols.NewToken()
	}
	webConfig := webext.Config{
		Database: opts.DB,
		InitialProfile: func(ctx context.Context) (profile.Profile, error) {
			p, err := initWebProfile(ctx, option, logger)
			if err != nil {
				return p, err
			}
			providers, err := p.Providers()
			if err != nil {
				return p, err
			}
			if model, _ := providers.Current(); model == nil {
				logger.Warnf("%s", telemetry.StartupLine("skip", "llm", "AI disabled: set api_key in cyber.yaml or env"))
			}
			return p, nil
		},
		ConfigAPI:   configAPI(),
		AccessKey:   accessKey,
		ConfigStore: &webConfigStore{explicit: configFile, runtime: option},
		BuildProfile: func(ctx context.Context, prepared *webservice.PreparedConfig) (profile.Profile, error) {
			candidateOption := cfg.Option{}
			if explicitOption != nil {
				candidateOption = *explicitOption
			}
			candidateOption.ConfigFile = configFile
			environment := cfg.Context{}
			if option.Context != nil {
				environment = *option.Context
			}
			data, err := os.ReadFile(prepared.RuntimePath)
			if err != nil {
				return nil, err
			}
			environment.Replacements = map[string][]byte{prepared.TargetPath: data}
			candidateOption.Context = &environment
			candidateOption.Sections = defaultSections()
			if _, err := cfg.ResolveRuntimeConfig(&candidateOption); err != nil {
				return nil, err
			}
			// The staged YAML is resolved into the flags config, which stays the
			// truth for the candidate runtime; the proto is only the settings
			// payload that produced it.
			candidateProfile, err := initWebProfile(ctx, &candidateOption, logger)
			if err != nil {
				return candidateProfile, err
			}
			return candidateProfile, nil
		},
		Scans: &webservice.ScanServiceConfig{
			MaxConcurrent: opts.MaxScans,
			ScanTimeout:   time.Duration(opts.ScanTimeout) * time.Second,
		},
	}
	if option.Debug {
		webConfig.AllowedOrigins = []string{"*"}
	}

	staticSub, err := fs.Sub(webstatic.FS, "static")
	if err != nil {
		return fmt.Errorf("load static assets: %s", err)
	}

	routes := webext.New(webConfig)
	ioaExtension := ioaserver.NewBrowser(ioaservice.Config{AccessKey: accessKey})
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

	httpHandler, err := web.NewHandler(routes.Service().Auth(), newSPAFileServer(staticSub), routes.Routes()...)
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
			if err := node.RunWebSocket(embeddedCtx, newAIScanProfile, &agentOption, logger); err != nil && ctx.Err() == nil {
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
	if err := applyIdentity(&option); err != nil {
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

func initWebProfile(ctx context.Context, baseOption *cfg.Option, logger telemetry.Logger) (*aiscanProfile, error) {
	option := cfg.Option{}
	if baseOption != nil {
		option = *baseOption
	}
	if option.Resolved == nil {
		resolved, err := defaultSections().ResolveValues(option.Extensions, nil, nil)
		if err != nil {
			return nil, err
		}
		option.Resolved = resolved
		option.Extensions = resolved.Values()
	}
	config, err := configFromOption(&option, profile.ProviderOptional, nil, logger)
	if err != nil {
		return nil, err
	}
	config.Base.SkipEngines, config.IOA = true, nil
	p, err := buildAIScanProfile(config)
	if err != nil {
		return nil, err
	}
	if err := p.Load(ctx); err != nil {
		return p, err
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// Config file store for web UI settings page
// ---------------------------------------------------------------------------

type webConfigStore struct {
	explicit string
	// runtime supplies the original discovery context; its overrides are never saved.
	runtime *cfg.Option
	mu      sync.Mutex
}

func (s *webConfigStore) configContext() *cfg.Context {
	if s.runtime != nil {
		return s.runtime.Context
	}
	return nil
}

func (s *webConfigStore) stored() (*cfg.Snapshot, bool, *types.DistributeConfig, error) {
	snapshot, err := cfg.LoadSnapshot(s.configContext(), s.explicit, defaultSections())
	if err != nil && errors.Is(err, os.ErrNotExist) && s.explicit != "" {
		discovered, e := cfg.Discover(s.configContext(), s.explicit)
		if e != nil {
			return nil, false, nil, e
		}
		environment := discovered.Context
		environment.Replacements = map[string][]byte{discovered.Target: []byte("{}")}
		snapshot, err = cfg.LoadSnapshot(&environment, s.explicit, defaultSections())
	}
	if err != nil {
		return nil, false, nil, err
	}
	data, err := yaml.Marshal(snapshot.RuntimeDocument(defaultSections()))
	if err != nil {
		return nil, false, nil, err
	}
	value, err := parseConfig(data)
	if err != nil {
		return nil, false, nil, err
	}
	fileOption, err := snapshot.FileOptions(defaultSections())
	if err != nil {
		return nil, false, nil, err
	}
	if len(fileOption.Providers) > 0 || cfg.HasSingleProviderFields(fileOption) {
		value.Llm = cfg.LLMFromOption(fileOption)
		cfg.NormalizeLLMConfig(value.Llm)
	}
	loaded := false
	for _, layer := range snapshot.Layers {
		if _, e := os.Stat(layer.Path); e == nil {
			loaded = true
		}
	}
	return snapshot, loaded, value, nil
}

func (s *webConfigStore) GetDistributeConfig(ctx context.Context) (string, bool, *types.DistributeConfig, error) {
	if err := ctx.Err(); err != nil {
		return "", false, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, loaded, value, err := s.stored()
	if err != nil {
		return "", false, nil, err
	}
	return snapshot.Target, loaded, value, nil
}

func (s *webConfigStore) PrepareDistributeConfig(ctx context.Context, incoming *types.DistributeConfig) (*webservice.PreparedConfig, error) {
	var err error
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot, _, current, err := s.stored()
	if err != nil {
		return nil, err
	}
	p := snapshot.Target
	incomingCopy := incoming
	if incomingCopy != nil {
		incoming = proto.Clone(incomingCopy).(*types.DistributeConfig)
	}
	if incoming == nil {
		incoming = &types.DistributeConfig{}
	}
	if incoming.Llm == nil {
		incoming.Llm = &types.LLMConfig{}
		if current.Llm != nil {
			incoming.Llm = proto.Clone(current.Llm).(*types.LLMConfig)
		}
	}
	cfg.NormalizeLLMConfig(incoming.Llm)
	if incoming.Agent == nil {
		incoming.Agent = current.Agent
	}
	if incoming.Traffic == nil {
		incoming.Traffic = current.Traffic
	}
	if incoming.Scan == nil {
		incoming.Scan = current.Scan
	}

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
	sections := defaultSections()
	nextValues, currentValues := cfg.ValuesFromProto(incoming.Extensions), cfg.ValuesFromProto(current.Extensions)
	preserveURLCredentials(nextValues, currentValues)
	incoming.Extensions, err = cfg.ValuesToProto(sections.Preserve(nextValues, currentValues))
	if err != nil {
		return nil, err
	}
	if err = validateConfig(incoming); err != nil {
		return nil, err
	}

	beforeBytes, err := marshalConfig(current, nil)
	if err != nil {
		return nil, err
	}
	afterBytes, err := marshalConfig(incoming, nil)
	if err != nil {
		return nil, err
	}
	var before, after map[string]any
	if err = yaml.Unmarshal(beforeBytes, &before); err != nil {
		return nil, err
	}
	if err = yaml.Unmarshal(afterBytes, &after); err != nil {
		return nil, err
	}
	target := snapshot.TargetDocument()
	// Convert a target's own shorthand when editing LLM settings. Never materialize inherited credentials.
	if !proto.Equal(current.GetLlm(), incoming.GetLlm()) {
		if raw, ok := target["llm"].(map[string]any); ok {
			if _, flat := raw["model"]; flat {
				original, _ := yaml.Marshal(target)
				own, parseErr := parseConfig(original)
				if parseErr != nil {
					return nil, parseErr
				}
				ownBytes, e := marshalConfig(own, nil)
				if e != nil {
					return nil, e
				}
				var ownDoc map[string]any
				if e = yaml.Unmarshal(ownBytes, &ownDoc); e != nil {
					return nil, e
				}
				target["llm"] = ownDoc["llm"]
			}
		}
	}
	// The target may have been converted from flat LLM shorthand above.
	for i := range snapshot.Layers {
		if snapshot.Layers[i].Path == snapshot.Target {
			snapshot.Layers[i].Document = target
		}
	}
	patched, err := snapshot.ApplyChanges(before, after)
	if err != nil {
		return nil, err
	}
	next, err := yaml.Marshal(patched)
	if err != nil {
		return nil, err
	}
	// Validate the merged candidate, not an isolated temporary config file.
	environment := snapshot.Context
	environment.Replacements = map[string][]byte{p: next}
	candidate, err := cfg.LoadSnapshot(&environment, s.explicit, defaultSections())
	if err != nil {
		return nil, err
	}
	candidateBytes, err := yaml.Marshal(candidate.RuntimeDocument(defaultSections()))
	if err != nil {
		return nil, err
	}
	merged, err := parseConfig(candidateBytes)
	if err != nil {
		return nil, err
	}
	fileOption, err := candidate.FileOptions(defaultSections())
	if err != nil {
		return nil, err
	}
	if len(fileOption.Providers) > 0 || cfg.HasSingleProviderFields(fileOption) {
		merged.Llm = cfg.LLMFromOption(fileOption)
		cfg.NormalizeLLMConfig(merged.Llm)
	}
	tmpPath, err := cfg.PrepareFile(p, next)
	if err != nil {
		return nil, err
	}
	return &webservice.PreparedConfig{Config: merged, RuntimePath: tmpPath, TargetPath: p}, nil
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
	if err := cfg.CommitFile(prepared.RuntimePath, prepared.TargetPath); err != nil {
		return err
	}
	prepared.RuntimePath = ""
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
		if profile.Id == "" && i < len(existingProviders) && existingProviders[i].GetId() == "" {
			profile.ApiKey = existingProviders[i].GetApiKey()
		}
	}
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
