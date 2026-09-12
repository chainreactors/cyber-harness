package ioa

import (
	"context"
	"errors"
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

// Extension owns one composition's IOA client, node registration retry, and command
// bindings. It is an optional concrete instance; the Agent runtime only borrows
// its typed client APIs for handoff and inbox delivery.
type Service struct {
	mu       sync.RWMutex
	config   Config
	commands *commands.Registry
	logger   telemetry.Logger

	client     *ioaclient.Client
	stream     ioaclient.StreamAPI
	cancel     context.CancelFunc
	retry      chan struct{}
	loaded     bool
	closed     bool
	attempted  bool
	registered bool
	binding    *spaceBinding
}

// New constructs an inert instance. Network registration and command
// publication begin in Load.
func New(config Config, commands *commands.Registry, logger telemetry.Logger) *Service {
	config.NodeMeta = cloneNodeMeta(config.NodeMeta)
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	return &Service{config: config, commands: commands, logger: logger, binding: &spaceBinding{}}
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
func (m *Service) Start(ctx context.Context) error {
	if m == nil {
		return fmt.Errorf("IOA instance is required")
	}
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
	if m.config.RegisterCommands && m.commands == nil {
		return fmt.Errorf("IOA command registry is required")
	}
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
	if m.config.RegisterCommands && m.commands != nil {
		root := &rootCommand{client: client, binding: m.binding, nodeName: m.config.NodeName, meta: m.config.NodeMeta}
		if err := m.commands.Register("ioa", "ioa", root.commands()...); err != nil {
			cancel()
			return err
		}
		m.registered = true
	}
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

func (m *Service) retryRegistration(ctx context.Context) {
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

func (m *Service) configureSpace(ctx context.Context) {
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

func (m *Service) Client() *ioaclient.Client {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.client
}

func (m *Service) Stream() ioaclient.StreamAPI {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stream
}

// Close cancels registration work and waits for the owned retry to finish.
func (m *Service) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
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
	registered := m.registered
	m.mu.Unlock()
	var unregisterErr error
	if registered {
		unregisterErr = m.commands.UnregisterOwner(ctx, "ioa")
	}
	if retry == nil {
		return unregisterErr
	}
	select {
	case <-retry:
		return unregisterErr
	default:
	}
	select {
	case <-retry:
		return unregisterErr
	case <-ctx.Done():
		return errors.Join(unregisterErr, ctx.Err())
	}
}
