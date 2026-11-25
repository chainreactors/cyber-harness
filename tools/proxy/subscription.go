package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/proxyclient"
	"github.com/chainreactors/proxyclient/extra/clash"
)

// SubscriptionConfig is shared by automatic configuration and the existing
// command/protocol adapters. An empty strategy loads nodes without routing.
type SubscriptionConfig struct {
	URL      string `json:"url"`
	Refresh  string `json:"refresh"`
	Proxy    string `json:"proxy"`
	Strategy string `json:"strategy"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Country  string `json:"country"`
}

func (c SubscriptionConfig) Validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("subscription url must use HTTP or HTTPS")
	}
	if c.Refresh != "" {
		interval, err := time.ParseDuration(c.Refresh)
		if err != nil || interval < 0 {
			return fmt.Errorf("refresh must be a nonnegative duration")
		}
	}
	if c.Proxy != "" {
		proxy, err := url.Parse(c.Proxy)
		if err != nil || proxy.Host == "" || (proxy.Scheme != "http" && proxy.Scheme != "https" && proxy.Scheme != "socks5" && proxy.Scheme != "socks5h") {
			return fmt.Errorf("subscription proxy must be an HTTP or SOCKS URL")
		}
	}
	switch clash.Strategy(c.Strategy) {
	case "", clash.StrategyAdaptive, clash.StrategyFirst, clash.StrategyRandom, clash.StrategyHash, clash.StrategyRoundRobin, clash.StrategyURLTest:
	default:
		return fmt.Errorf("unknown subscription strategy %q", c.Strategy)
	}
	return nil
}

// Subscription owns fetching and refresh work, not the proxy's route lifetime.
type Subscription struct {
	state    *State
	config   SubscriptionConfig
	client   *http.Client
	cancel   context.CancelFunc
	done     chan struct{}
	revision uint64
	mu       sync.Mutex
	success  time.Time
	err      error
}

func NewSubscription(init, lifetime context.Context, state *State, config SubscriptionConfig) (*Subscription, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if config.Proxy != "" {
		proxy, err := url.Parse(config.Proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid subscription proxy")
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	ctx, cancel := context.WithCancel(lifetime)
	s := &Subscription{state: state, config: config, cancel: cancel, done: make(chan struct{}), client: &http.Client{
		Transport: transport, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	// Initial fetch obeys both startup cancellation and the owner's lifetime.
	initial, stop := context.WithCancel(init)
	unwatch := context.AfterFunc(ctx, stop)
	err := s.refresh(initial, true)
	unwatch()
	stop()
	if err != nil {
		cancel()
		transport.CloseIdleConnections()
		return nil, err
	}
	interval, _ := time.ParseDuration(config.Refresh)
	go func() {
		defer close(s.done)
		defer transport.CloseIdleConnections()
		if interval == 0 {
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.refresh(ctx, false); errors.Is(err, errSubscriptionReplaced) {
					return
				}
			}
		}
	}()
	return s, nil
}

var errSubscriptionReplaced = errors.New("subscription was replaced")

func (s *Subscription) refresh(ctx context.Context, initial bool) (err error) {
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.err = err
		if err == nil {
			s.success = time.Now()
		}
	}()
	if !initial {
		s.state.mu.RLock()
		current := s.state.revision == s.revision
		s.state.mu.RUnlock()
		if !current {
			return errSubscriptionReplaced
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.config.URL, nil)
	if err != nil {
		return fmt.Errorf("invalid subscription request")
	}
	req.Header.Set("User-Agent", clashSubscriptionUA)
	resp, err := s.client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return fmt.Errorf("fetch subscription: %w", urlErr.Err)
		}
		return fmt.Errorf("fetch subscription failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("subscription returned HTTP %d", resp.StatusCode)
	}
	const maxSize = 8 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return fmt.Errorf("read subscription: %w", err)
	}
	if len(data) > maxSize {
		return fmt.Errorf("subscription exceeds 8 MiB")
	}
	sub, err := clash.ParseSubscription(data)
	if err != nil {
		return err
	}
	var dial proxyclient.Dial
	if s.config.Strategy != "" {
		opts := clash.Options{Strategy: clash.Strategy(s.config.Strategy)}
		var filters []func(clash.ProxyNode) bool
		if s.config.Type != "" {
			filters = append(filters, clash.FilterByType(strings.Split(s.config.Type, ",")...))
		}
		if s.config.Name != "" {
			filters = append(filters, clash.FilterByName(s.config.Name))
		}
		if s.config.Country != "" {
			filters = append(filters, clash.FilterByCountry(strings.Split(s.config.Country, ",")...))
		}
		opts.Filter = func(node clash.ProxyNode) bool {
			if ctx.Err() != nil {
				return false
			}
			for _, filter := range filters {
				if !filter(node) {
					return false
				}
			}
			return true
		}
		dial, _, err = clash.NewDialerFromSubscription(sub, opts)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state := s.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !initial && state.revision != s.revision {
		return errSubscriptionReplaced
	}
	if !initial && state.activeNode != nil {
		var selected *clash.ProxyNode
		for i := range sub.Nodes {
			node := &sub.Nodes[i]
			if node.Name == state.activeNode.Name && node.URL != nil && node.URL.String() == state.activeURL {
				selected = node
				break
			}
		}
		if selected == nil {
			return fmt.Errorf("selected node changed; retaining previous subscription")
		}
		state.activeNode = selected
	} else if initial {
		state.activeNode, state.activeURL, state.singleDial = nil, "", nil
		state.autoDial, state.autoURL = dial, ""
		if dial != nil {
			state.autoURL = "clash://subscription"
		}
	} else if state.autoDial != nil {
		state.autoDial = dial
	}
	state.subscription, state.subscribeURL = sub, s.config.URL
	if initial {
		state.revision++
		s.revision = state.revision
	}
	state.publishChainLocked()
	return nil
}

func (s *Subscription) Status() (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.success, s.err
}

func (s *Subscription) Close(ctx context.Context) error {
	s.cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Subscribe is the one-shot path used by the existing command and protocol.
func (s *State) Subscribe(ctx context.Context, config SubscriptionConfig) error {
	config.Refresh = ""
	sub, err := NewSubscription(ctx, ctx, s, config)
	if err != nil {
		return err
	}
	return sub.Close(ctx)
}
