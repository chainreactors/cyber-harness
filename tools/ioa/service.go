package ioa

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	ioaclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/ioa/protocols"
	ioaserver "github.com/chainreactors/ioa/server"
)

// sdkClient collects only capabilities consumed by the IOA integration.
type sdkClient interface {
	ioaclient.StreamAPI
	Reader
	Bound() bool
	EnsureRegistered(context.Context, string, string, map[string]any) error
}

type Config struct {
	URL              string
	Token            string
	NodeID           string
	NodeName         string
	Space            string
	RegisterCommands bool
	AutoRegister     bool
	NodeMeta         map[string]any
	Identity         protocols.Identity
}

// Service exposes IOA queries and collaboration status. It has no
// lifecycle operations; Resource is the sole owner of startup and shutdown.
type Service struct {
	mu     sync.RWMutex
	config Config
	logger telemetry.Logger

	client      sdkClient
	beforeSpace func(context.Context, string) error
	closeStore  func() error
	closeErr    error
	cancel      context.CancelFunc
	retry       chan struct{}
	loaded      bool
	closed      bool
	attempted   bool
	binding     *spaceBinding
	ready       chan struct{}
	lifetime    context.Context
	statusMu    sync.Mutex
	lastError   error
	dropped     uint64
	queries     sync.WaitGroup
	done        chan struct{}
}

// Resource owns one Service's client connection and registration retry. The
// IOA extension retains this value and publishes only Service.
type Resource struct {
	Service *Service
}

// New constructs an inert instance. Network work begins in Start.
func New(config Config, logger telemetry.Logger) *Resource {
	config.NodeMeta = cloneNodeMeta(config.NodeMeta)
	if config.NodeName == "" {
		config.NodeName = "cyber"
	}
	if config.URL == "" && config.Space == "" {
		config.Space = "default"
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	return &Resource{Service: &Service{config: config, logger: logger, binding: &spaceBinding{}, ready: make(chan struct{})}}
}

func cloneNodeMeta(source map[string]any) map[string]any {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

// Start binds and registers the configured client. ctx bounds initial setup;
// registration retries use the instance lifetime and stop in Close.
func (r *Resource) Start(ctx context.Context) error {
	if r == nil || r.Service == nil {
		return fmt.Errorf("IOA instance is required")
	}
	s := r.Service
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.attempted && !s.loaded {
		return fmt.Errorf("IOA instance is closed")
	}
	if s.loaded {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.attempted = true
	client, closeStore, err := newIOAClient(s.config)
	if err != nil {
		return err
	}
	s.closeStore = closeStore
	if remote, ok := client.(*ioaclient.Client); ok && s.config.Identity != nil {
		if err := remote.Bind(s.config.Identity); err != nil {
			return fmt.Errorf("bind ioa identity: %w", err)
		}
	}
	lifetime, cancel := context.WithCancel(context.Background())
	s.client, s.cancel = client, cancel
	s.lifetime = lifetime
	if err := s.setup(ctx); err != nil {
		if s.config.URL == "" {
			cancel()
			return err
		}
		if ctx.Err() != nil {
			cancel()
			return ctx.Err()
		}
		s.ReportError(err)
		s.retry = make(chan struct{})
		telemetry.SafeGo("ioa-registration-retry", func() {
			defer close(s.retry)
			s.retryRegistration(lifetime)
		})
	} else {
		close(s.ready)
	}
	s.loaded = true
	return nil
}

func (s *Service) setup(ctx context.Context) error {
	if s.config.AutoRegister || s.config.URL == "" {
		if err := s.client.EnsureRegistered(ctx, s.config.NodeName, "", s.config.NodeMeta); err != nil {
			return err
		}
	}
	return s.configureSpace(ctx)
}

func (s *Service) retryRegistration(ctx context.Context) {
	for attempt := 0; ; attempt++ {
		timer := time.NewTimer(registrationRetryDelay(attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := s.setup(ctx); err != nil {
			s.ReportError(err)
			continue
		}
		close(s.ready)
		return
	}
}

func registrationRetryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= 4 {
		return 10 * time.Second
	}
	return time.Second << uint(attempt)
}

func (s *Service) configureSpace(ctx context.Context) error {
	if s.config.Space == "" || s.client == nil || !s.client.Bound() {
		return nil
	}
	info, err := s.client.Space(ctx, s.config.Space, "cyber agent")
	if err != nil {
		return err
	}
	s.binding.initialize(info.ID)
	return nil
}

// WaitReady waits for configured registration and space resolution.
func (s *Service) WaitReady(ctx context.Context) error {
	s.mu.RLock()
	lifetime := s.lifetime
	closed := s.closed
	s.mu.RUnlock()
	if closed || lifetime == nil {
		return fmt.Errorf("IOA client is unavailable")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-lifetime.Done():
		return lifetime.Err()
	case <-s.ready:
		return nil
	}
}

func (s *Service) ReceiveSpace() string { return s.binding.get() }

type Status struct {
	Bound     bool
	Space     string
	LastError string
	Dropped   uint64
}

func (s *Service) Status() Status {
	if s == nil {
		return Status{}
	}
	s.mu.RLock()
	status := Status{Space: s.config.Space, Bound: !s.closed && s.client != nil && s.client.Bound()}
	s.mu.RUnlock()
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if s.lastError != nil {
		status.LastError = s.lastError.Error()
	}
	status.Dropped = s.dropped
	return status
}

// ReportError records a collaboration failure without changing Agent execution.
func (s *Service) ReportError(err error) {
	if err == nil {
		return
	}
	s.statusMu.Lock()
	s.lastError = err
	s.statusMu.Unlock()
	s.logger.Warnf("ioa: %s", err)
}

func (s *Service) ReportDropped(count uint64) {
	s.statusMu.Lock()
	s.dropped = count
	s.statusMu.Unlock()
}

func newIOAClient(config Config) (sdkClient, func() error, error) {
	if config.URL == "" {
		store, err := ioaserver.NewSQLiteStore(":memory:")
		if err != nil {
			return nil, nil, err
		}
		return ioaserver.NewLocalClient(ioaserver.NewService(store, ""), config.NodeID), store.Close, nil
	}
	var client *ioaclient.Client
	var err error
	if config.Token != "" {
		client, err = ioaclient.NewClientWithToken(config.URL, config.Token)
	} else {
		client, err = ioaclient.NewClient(config.URL, config.NodeID)
	}
	return client, nil, err
}

// Client lends streaming operations to collaboration. The returned interface
// has no connection lifecycle operations.
func (s *Service) Client() ioaclient.StreamAPI {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

func (s *Service) Commands() []coretool.Command {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.client == nil || s.closed {
		return nil
	}
	root := &rootCommand{beforeSpace: s.changeSpace, client: s.client, binding: s.binding, nodeName: s.config.NodeName, meta: s.config.NodeMeta}
	return root.commands()
}

// Close rejects new queries, cancels requests and registration, and joins them.
func (r *Resource) Close(ctx context.Context) error {
	if r == nil || r.Service == nil {
		return nil
	}
	s := r.Service
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		if s.cancel != nil {
			s.cancel()
		}
		s.done = make(chan struct{})
		go func() {
			if s.retry != nil {
				<-s.retry
			}
			s.queries.Wait()
			if s.closeStore != nil {
				s.closeErr = s.closeStore()
			}
			close(s.done)
		}()
	}
	done := s.done
	s.mu.Unlock()
	select {
	case <-done:
		return s.closeErr
	default:
	}
	select {
	case <-done:
		return s.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SetSpaceChangeHandler installs the owning extension's subscription boundary before commands are exposed.
func (s *Service) SetSpaceChangeHandler(fn func(context.Context, string) error) {
	s.mu.Lock()
	s.beforeSpace = fn
	s.mu.Unlock()
}

// ReadySpace captures a binding only after its subscription is established.
// It shares the command switch lock, so starting a task cannot restore a stale feed.
func (s *Service) ReadySpace(ctx context.Context) (string, error) {
	if err := s.WaitReady(ctx); err != nil {
		return "", err
	}
	s.binding.mu.Lock()
	defer s.binding.mu.Unlock()
	space := s.binding.spaceID
	if space == "" {
		return "", fmt.Errorf("IOA space is not configured")
	}
	if err := s.changeSpace(ctx, space); err != nil {
		return "", err
	}
	return space, nil
}

func (s *Service) changeSpace(ctx context.Context, space string) error {
	s.mu.RLock()
	handler := s.beforeSpace
	s.mu.RUnlock()
	if handler != nil {
		return handler(ctx, space)
	}
	return nil
}
