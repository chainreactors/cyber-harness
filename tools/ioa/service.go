package ioa

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	ioaclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/ioa/protocols"
)

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

// Runtime exposes IOA queries and collaboration status. It has no
// lifecycle operations; Resource is the sole owner of startup and shutdown.
type Runtime struct {
	mu     sync.RWMutex
	config Config
	logger telemetry.Logger

	client       *ioaclient.Client
	cancel       context.CancelFunc
	retry        chan struct{}
	loaded       bool
	closed       bool
	attempted    bool
	binding      *spaceBinding
	receiveSpace *spaceBinding
	ready        chan struct{}
	lifetime     context.Context
	statusMu     sync.Mutex
	lastError    error
	dropped      uint64
	queries      sync.WaitGroup
	done         chan struct{}
}

// Resource owns one Runtime's client connection and registration retry. The
// IOA extension retains this value and publishes only Runtime.
type Resource struct {
	Runtime *Runtime
}

// New constructs an inert instance. Network work begins in Start.
func New(config Config, logger telemetry.Logger) *Resource {
	config.NodeMeta = cloneNodeMeta(config.NodeMeta)
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	return &Resource{Runtime: &Runtime{config: config, logger: logger, binding: &spaceBinding{}, receiveSpace: &spaceBinding{}, ready: make(chan struct{})}}
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

// Load binds and registers the configured client. ctx bounds initial setup;
// registration retries use the instance lifetime and stop in Close.
func (r *Resource) Start(ctx context.Context) error {
	if r == nil || r.Runtime == nil {
		return fmt.Errorf("IOA instance is required")
	}
	m := r.Runtime
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.attempted && !m.loaded {
		return fmt.Errorf("IOA instance is closed")
	}
	if m.loaded {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.attempted = true
	client, err := newIOAClient(m.config)
	if err != nil {
		return err
	}
	if client == nil {
		m.loaded = true
		return nil
	}
	if m.config.Identity != nil {
		if err := client.Bind(m.config.Identity); err != nil {
			return fmt.Errorf("bind ioa identity: %w", err)
		}
	}
	lifetime, cancel := context.WithCancel(context.Background())
	m.client, m.cancel = client, cancel
	m.lifetime = lifetime
	if err := m.setup(ctx); err != nil {
		if ctx.Err() != nil {
			cancel()
			return ctx.Err()
		}
		m.ReportError(err)
		m.retry = make(chan struct{})
		telemetry.SafeGo("ioa-registration-retry", func() {
			defer close(m.retry)
			m.retryRegistration(lifetime)
		})
	} else {
		close(m.ready)
	}
	m.loaded = true
	return nil
}

func (m *Runtime) setup(ctx context.Context) error {
	if m.config.AutoRegister {
		if err := m.client.EnsureRegistered(ctx, m.config.NodeName, "", m.config.NodeMeta); err != nil {
			return err
		}
	}
	return m.configureSpace(ctx)
}

func (m *Runtime) retryRegistration(ctx context.Context) {
	for attempt := 0; ; attempt++ {
		timer := time.NewTimer(registrationRetryDelay(attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := m.setup(ctx); err != nil {
			m.ReportError(err)
			continue
		}
		close(m.ready)
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

func (m *Runtime) configureSpace(ctx context.Context) error {
	if m.config.Space == "" || m.client == nil || !m.client.Bound() {
		return nil
	}
	info, err := m.client.Space(ctx, m.config.Space, "aiscan agent")
	if err != nil {
		return err
	}
	m.binding.initialize(info.ID)
	m.receiveSpace.set(info.ID)
	return nil
}

// WaitReady waits for configured registration and space resolution.
func (m *Runtime) WaitReady(ctx context.Context) error {
	m.mu.RLock()
	lifetime := m.lifetime
	closed := m.closed
	m.mu.RUnlock()
	if closed || lifetime == nil {
		return fmt.Errorf("IOA client is unavailable")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-lifetime.Done():
		return lifetime.Err()
	case <-m.ready:
		return nil
	}
}

func (m *Runtime) ReceiveSpace() string { return m.receiveSpace.get() }

type Status struct {
	Bound     bool
	Space     string
	LastError string
	Dropped   uint64
}

func (m *Runtime) Status() Status {
	if m == nil {
		return Status{}
	}
	m.mu.RLock()
	status := Status{Space: m.config.Space, Bound: !m.closed && m.client != nil && m.client.Bound()}
	m.mu.RUnlock()
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	if m.lastError != nil {
		status.LastError = m.lastError.Error()
	}
	status.Dropped = m.dropped
	return status
}

// ReportError records a collaboration failure without changing Agent execution.
func (m *Runtime) ReportError(err error) {
	if err == nil {
		return
	}
	m.statusMu.Lock()
	m.lastError = err
	m.statusMu.Unlock()
	m.logger.Warnf("ioa: %s", err)
}

func (m *Runtime) ReportDropped(count uint64) {
	m.statusMu.Lock()
	m.dropped = count
	m.statusMu.Unlock()
}

func newIOAClient(config Config) (*ioaclient.Client, error) {
	if config.URL == "" {
		return nil, nil
	}
	if config.Token != "" {
		return ioaclient.NewClientWithToken(config.URL, config.Token)
	}
	return ioaclient.NewClient(config.URL, config.NodeID)
}

// Client lends the SDK handle to the owning extension. Hosts receive Runtime
// or Reader and cannot acquire the underlying SDK client.
func (r *Resource) Client() *ioaclient.Client {
	if r == nil || r.Runtime == nil {
		return nil
	}
	m := r.Runtime
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.client
}

func (r *Resource) Commands() []commands.Command {
	if r == nil || r.Runtime == nil {
		return nil
	}
	m := r.Runtime
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.client == nil || m.closed {
		return nil
	}
	root := &rootCommand{client: m.client, binding: m.binding, nodeName: m.config.NodeName, meta: m.config.NodeMeta}
	return root.commands()
}

// Close rejects new queries, cancels requests and registration, and joins them.
func (r *Resource) Close(ctx context.Context) error {
	if r == nil || r.Runtime == nil {
		return nil
	}
	m := r.Runtime
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		if m.cancel != nil {
			m.cancel()
		}
		m.done = make(chan struct{})
		go func() {
			if m.retry != nil {
				<-m.retry
			}
			m.queries.Wait()
			close(m.done)
		}()
	}
	done := m.done
	m.mu.Unlock()
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
