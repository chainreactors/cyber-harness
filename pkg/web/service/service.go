package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/runner"
	types "github.com/chainreactors/aiscan/pkg/types"
	web "github.com/chainreactors/aiscan/pkg/web"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ConfigStore and PreparedConfig are the host integration surface for config
// persistence; the business semantics live in web/api.
type ConfigStore = managementapi.ConfigStore
type PreparedConfig = managementapi.PreparedConfig

type ServiceConfig struct {
	Store         *SQLiteStore
	App           *runner.App
	ConfigStore   ConfigStore
	AppFactory    func(ctx context.Context, prepared *PreparedConfig) (*runner.App, error)
	AgentPool     *AgentPool
	Artifacts     ArtifactIngestor
	MaxConcurrent int
	ScanTimeout   time.Duration
	AccessKey     string
}

type Service struct {
	store   *SQLiteStore
	appMu   sync.Mutex
	app     *managedApp
	api     *managementapi.API
	auth    *Auth
	agents  *AgentPool
	hub     *Hub
	sem     chan struct{}
	timeout time.Duration

	mu           sync.Mutex
	cancels      map[string]context.CancelFunc
	scanNodeIDs  map[string]string
	taskSessions map[string]string // taskID → sessionID
	taskNodeIDs  map[string]string // taskID → nodeID
	taskCanceled map[string]bool

	eventMu    sync.Mutex
	sessionSeq map[string]uint64
	endedTurns map[string]bool
}

func NewService(cfg ServiceConfig) *Service {
	maxConcurrent := cfg.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	timeout := cfg.ScanTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	svc := &Service{
		store:        cfg.Store,
		app:          wrapManagedApp(cfg.App),
		agents:       cfg.AgentPool,
		hub:          NewHub(),
		sem:          make(chan struct{}, maxConcurrent),
		timeout:      timeout,
		auth:         NewAuth(cfg.AccessKey),
		cancels:      make(map[string]context.CancelFunc),
		scanNodeIDs:  make(map[string]string),
		taskSessions: make(map[string]string),
		taskNodeIDs:  make(map[string]string),
		taskCanceled: make(map[string]bool),
		sessionSeq:   make(map[string]uint64),
		endedTurns:   make(map[string]bool),
	}
	configAPI := managementapi.NewConfig(managementapi.ConfigOptions{
		Store: cfg.ConfigStore,
		Build: cfg.AppFactory,
		Apply: svc.swapApp,
		Broadcast: func(config *types.DistributeConfig) {
			if svc.agents != nil {
				svc.agents.BroadcastConfigReload(config)
			}
		},
	})
	svc.api = &managementapi.API{
		Sessions:  managementapi.NewSessions(cfg.Store, svc, generateID),
		Config:    configAPI,
		Scans:     managementapi.NewScans(svc, svc.hub),
		SCO:       managementapi.NewSCO(cfg.Store, cfg.Artifacts),
		Status:    svc,
		ServerURL: "/",
	}
	if cfg.AgentPool != nil {
		svc.api.Agents = cfg.AgentPool
		cfg.AgentPool.SetSessionLookup(svc)
	}
	return svc
}

func (s *Service) Hub() *Hub { return s.hub }

func (s *Service) SetAgentPool(pool *AgentPool) {
	s.agents = pool
	if s.api != nil {
		if pool == nil {
			s.api.Agents = nil
		} else {
			s.api.Agents = pool
		}
	}
	if pool == nil {
		return
	}
	pool.SetSessionLookup(s)
	pool.config = s.api.Config.Distribute
}

func (s *Service) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.cancels))
	for _, cancel := range s.cancels {
		cancels = append(cancels, cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	s.appMu.Lock()
	current := s.app
	s.app = nil
	app := retireManagedApp(current)
	s.appMu.Unlock()
	if app != nil {
		app.Close()
	}
}

// API exposes the existing business service composition to transport adapters.
func (s *Service) API() *managementapi.API {
	if s == nil {
		return nil
	}
	return s.api
}

// Auth returns the authentication mechanism shared by HTTP, ConnectRPC and
// WebSocket bindings.
func (s *Service) Auth() web.Auth {
	if s == nil || s.auth == nil {
		return NewAuth("")
	}
	return s.auth
}

var _ web.Service = (*Service)(nil)

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func nowProto() *timestamppb.Timestamp { return timestamppb.New(time.Now()) }

func (s *Service) Status() *types.SystemStatus {
	app, release := s.acquireApp()
	status := &types.SystemStatus{
		Version:      config.Version,
		LlmAvailable: app != nil && app.Provider != nil,
	}
	if app != nil {
		status.LlmProvider = app.ProviderConfig.Provider
		status.LlmModel = app.ProviderConfig.Model
		status.LlmApiKeyConfigured = strings.TrimSpace(app.ProviderConfig.APIKey) != ""
	}
	release()
	if response, err := s.api.Config.GetConfig(context.Background(), &types.GetConfigRequest{}); err == nil {
		view := response.GetConfig()
		status.ConfigPath = view.GetPath()
		status.ConfigLoaded = view.GetLoaded()
		if active := view.GetLlm().GetActive(); active != nil {
			if status.LlmProvider == "" {
				status.LlmProvider = active.GetProvider()
			}
			if status.LlmModel == "" {
				status.LlmModel = active.GetModel()
			}
			status.LlmApiKeyConfigured = status.LlmApiKeyConfigured || active.GetApiKeyConfigured()
		}
	}
	return status
}
