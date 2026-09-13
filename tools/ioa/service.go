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
	NodeID           string
	NodeName         string
	Space            string
	RegisterCommands bool
	AutoRegister     bool
	NodeMeta         map[string]any
	Identity         protocols.Identity
}

// Runtime exposes the live IOA client APIs used by Agent sessions. It has no
// lifecycle operations; Resource is the sole owner of startup and shutdown.
type Runtime struct {
	mu     sync.RWMutex
	config Config
	logger telemetry.Logger

	client    *ioaclient.Client
	stream    ioaclient.StreamAPI
	cancel    context.CancelFunc
	retry     chan struct{}
	loaded    bool
	closed    bool
	attempted bool
	binding   *spaceBinding
}

// Resource owns one Runtime's client connection and registration retry. The
// IOA extension retains this value and publishes only Runtime.
type Resource struct {
	*Runtime
}

// New constructs an inert instance. Network work begins in Start.
func New(config Config, logger telemetry.Logger) *Resource {
	config.NodeMeta = cloneNodeMeta(config.NodeMeta)
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	return &Resource{Runtime: &Runtime{config: config, logger: logger, binding: &spaceBinding{}}}
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
	m.client, m.stream, m.cancel = client, client, cancel
	if m.config.AutoRegister {
		if err := client.EnsureRegistered(ctx, m.config.NodeName, "", m.config.NodeMeta); err != nil {
			if ctx.Err() != nil {
				cancel()
				return ctx.Err()
			}
			m.logger.Warnf("ioa registration pending: %s", err)
			m.retry = make(chan struct{})
			telemetry.SafeGo("ioa-registration-retry", func() {
				defer close(m.retry)
				m.retryRegistration(lifetime)
			})
			m.loaded = true
			return nil
		}
	}
	m.configureSpace(ctx)
	m.loaded = true
	return nil
}

func (m *Runtime) retryRegistration(ctx context.Context) {
	for attempt := 0; ; attempt++ {
		delay := registrationRetryDelay(attempt)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if m.client.EnsureRegistered(ctx, m.config.NodeName, "", m.config.NodeMeta) == nil {
			m.logger.Infof("ioa node registered: %s", m.client.NodeID())
			m.configureSpace(ctx)
			return
		}
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

func (m *Runtime) configureSpace(ctx context.Context) {
	if m.config.Space == "" || m.client == nil || !m.client.Bound() {
		return
	}
	info, err := m.client.Space(ctx, m.config.Space, "aiscan agent")
	if err != nil {
		return
	}
	m.binding.set(info.ID)
}

func newIOAClient(config Config) (*ioaclient.Client, error) {
	if config.URL == "" {
		return nil, nil
	}
	return ioaclient.NewClient(config.URL, config.NodeID)
}

func (m *Runtime) Client() *ioaclient.Client {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.client
}

func (m *Runtime) Stream() ioaclient.StreamAPI {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stream
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

// Close cancels registration work and waits for the owned retry to finish.
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
	}
	retry := m.retry
	m.mu.Unlock()
	if retry == nil {
		return nil
	}
	select {
	case <-retry:
		return nil
	default:
	}
	select {
	case <-retry:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
