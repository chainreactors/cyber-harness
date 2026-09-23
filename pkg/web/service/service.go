package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/cyber/core/extension"
	types "github.com/chainreactors/cyber/core/types"
	"github.com/chainreactors/cyber/pkg/config"
	profile "github.com/chainreactors/cyber/pkg/profile"
	web "github.com/chainreactors/cyber/pkg/web"
	managementapi "github.com/chainreactors/cyber/pkg/web/api"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ScanServiceConfig enables the scan console. A nil Scans field disables it:
// no scan routes, no scan dispatch namespace, and no session scan bindings.
type ScanServiceConfig struct {
	MaxConcurrent int
	ScanTimeout   time.Duration
}

type ServiceConfig struct {
	ConfigAPI   managementapi.ConfigOptions
	Store       *SQLiteStore
	Profile     profile.Profile
	ConfigStore ConfigStore
	// BuildProfile returns a fresh candidate, including partial results on error.
	// Service owns every returned candidate and its cleanup.
	BuildProfile func(ctx context.Context, prepared *PreparedConfig) (profile.Profile, error)
	Scans        *ScanServiceConfig
	AccessKey    string
}

type Service struct {
	workContext context.Context
	stopWork    context.CancelFunc
	work        sync.WaitGroup
	workDone    chan struct{}
	// configGate serializes update/activation with shutdown. Service owns both
	// candidate and published profiles throughout the transaction.
	configGate   chan struct{}
	configStore  ConfigStore
	buildProfile func(context.Context, *PreparedConfig) (profile.Profile, error)
	pending      profile.Profile
	store        *SQLiteStore
	appMu        sync.Mutex
	profile      profile.Profile
	closing      bool
	api          *managementapi.API
	auth         *Auth
	agents       *AgentPool
	hub          *Hub
	sem          chan struct{}
	timeout      time.Duration

	mu           sync.Mutex
	cancels      map[string]context.CancelFunc
	scanNodeIDs  map[string]string
	taskSessions map[string]string // taskID → sessionID

	eventMu    sync.Mutex
	sessionSeq map[string]uint64
	endedTurns map[string]bool
}

func NewService(cfg ServiceConfig) *Service {
	workContext, stopWork := context.WithCancel(context.Background())
	svc := &Service{
		workContext: workContext, stopWork: stopWork,
		configGate:   make(chan struct{}, 1),
		configStore:  cfg.ConfigStore,
		buildProfile: cfg.BuildProfile,
		store:        cfg.Store,
		hub:          NewHub(),
		auth:         NewAuth(cfg.AccessKey),
		cancels:      make(map[string]context.CancelFunc),
		scanNodeIDs:  make(map[string]string),
		taskSessions: make(map[string]string),
		sessionSeq:   make(map[string]uint64),
		endedTurns:   make(map[string]bool),
	}
	if cfg.Profile != nil {
		svc.profile = cfg.Profile
	}
	configAPI := managementapi.NewConfig(svc, cfg.ConfigAPI)
	svc.api = &managementapi.API{
		Sessions:  managementapi.NewSessions(cfg.Store, svc, generateID),
		Config:    configAPI,
		Artifacts: managementapi.NewArtifacts(cfg.Store),
		Status:    svc,
		ServerURL: "/",
	}
	if cfg.Scans != nil {
		maxConcurrent := cfg.Scans.MaxConcurrent
		if maxConcurrent <= 0 {
			maxConcurrent = 3
		}
		timeout := cfg.Scans.ScanTimeout
		if timeout <= 0 {
			timeout = 10 * time.Minute
		}
		svc.sem = make(chan struct{}, maxConcurrent)
		svc.timeout = timeout
		svc.api.Scans = managementapi.NewScans(svc, svc.hub)
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

func (s *Service) Close(ctx context.Context) (resultErr error) {
	if s == nil {
		return nil
	}
	select {
	case s.configGate <- struct{}{}:
	default:
		select {
		case s.configGate <- struct{}{}:
		case <-ctx.Done():
			return errors.Join(extension.ErrCloseIncomplete, ctx.Err())
		}
	}
	defer func() { <-s.configGate }()
	s.appMu.Lock()
	if !s.closing {
		s.stopWork()
	}
	s.closing = true
	if s.workDone == nil {
		s.workDone = make(chan struct{})
		go func() { s.work.Wait(); close(s.workDone) }()
	}
	p := s.profile
	s.appMu.Unlock()
	select {
	case <-s.workDone:
	case <-ctx.Done():
		return errors.Join(extension.ErrCloseIncomplete, ctx.Err())
	}
	resultErr = s.closePending(ctx)
	if p != nil {
		err := p.Close(ctx)
		resultErr = errors.Join(resultErr, err)
		if !errors.Is(err, extension.ErrCloseIncomplete) {
			s.appMu.Lock()
			s.profile = nil
			s.appMu.Unlock()
		}
	}
	return resultErr
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
	providers := s.providers()
	status := &types.SystemStatus{Version: config.Version}
	if providers != nil {
		model, providerConfig := providers.Current()
		status.LlmAvailable = model != nil
		status.LlmProvider = providerConfig.Provider
		status.LlmModel = providerConfig.Model
		status.LlmApiKeyConfigured = strings.TrimSpace(providerConfig.APIKey) != ""
	}
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

func (s *Service) beginWork() (context.Context, bool) {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	if s.closing || s.workContext.Err() != nil {
		return nil, false
	}
	s.work.Add(1)
	return s.workContext, true
}
