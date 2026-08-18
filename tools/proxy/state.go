package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chainreactors/proxyclient"
	"github.com/chainreactors/proxyclient/extra/clash"
)

type State struct {
	mu            sync.RWMutex
	originalProxy string
	subscription  *clash.Subscription
	subscribeURL  string
	activeNode    *clash.ProxyNode
	activeURL     string
	autoURL       string           // clash:// URL for auto mode
	autoDial      proxyclient.Dial // pre-built dial for auto mode
	singleDial    proxyclient.Dial // pre-built dial for a single persistent proxy URL

	// chain is the composed egress dial for the current selection, republished
	// on every state change. ProxyHub's upstream reads it on each connection so
	// switching nodes takes effect live without touching in-flight children.
	chain atomic.Pointer[proxyclient.Dial]
}

func NewState(originalProxy string) *State {
	s := &State{originalProxy: originalProxy}
	s.publishChainLocked()
	return s
}

// CurrentDial returns the egress dial for the active proxy selection, or a
// direct dial when nothing is selected. It is lock-free (single atomic load)
// so ProxyHub can call it on every outbound connection.
func (s *State) CurrentDial() proxyclient.Dial {
	if p := s.chain.Load(); p != nil && *p != nil {
		return *p
	}
	return proxyclient.DefaultDial
}

// WithOverrideDial temporarily republishes the egress chain to route through
// proxyURL, returning a restore func. It backs the one-shot `proxy <url> <cmd>`
// override: children keep pointing at the stable hub address while the hub's
// upstream is swapped for the duration of the wrapped command.
func (s *State) WithOverrideDial(proxyURL string) (func(), error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	d, err := proxyclient.NewClient(u)
	if err != nil {
		return nil, fmt.Errorf("create proxy client: %w", err)
	}
	prev := s.chain.Load()
	dl := proxyclient.Dial(d)
	s.chain.Store(&dl)
	return func() { s.chain.Store(prev) }, nil
}

// publishChainLocked rebuilds the egress dial from the current selection and
// republishes it atomically. Callers that mutate selection fields already hold
// s.mu; it only reads those fields and stores into the lock-free atomic, so it
// must not take s.mu itself.
//
// Chain base is always DefaultDial (direct from the runner host): ProxyHub is
// the front hop, and the proxy nodes are its upstream. The clash auto dial is
// used as-is because its own base is DefaultDial, which is exactly this host.
func (s *State) publishChainLocked() {
	var dial proxyclient.Dial
	switch {
	case s.autoDial != nil:
		dial = s.autoDial
	case s.singleDial != nil:
		dial = s.singleDial
	case s.activeNode != nil && s.activeNode.URL != nil:
		if d, err := proxyclient.NewClient(s.activeNode.URL); err == nil {
			dial = d
		}
	case s.originalProxy != "":
		if u, err := url.Parse(s.originalProxy); err == nil {
			if d, err := proxyclient.NewClient(u); err == nil {
				dial = d
			}
		}
	}
	if dial == nil {
		dial = proxyclient.DefaultDial
	}
	s.chain.Store(&dial)
}

func (s *State) LoadSubscription(sub *clash.Subscription, subscribeURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscription = sub
	s.subscribeURL = subscribeURL
	s.activeNode = nil
	s.activeURL = ""
}

func (s *State) Nodes() []clash.ProxyNode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.subscription == nil {
		return nil
	}
	return s.subscription.Nodes
}

func (s *State) Switch(nameOrIndex string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subscription == nil {
		return fmt.Errorf("no subscription loaded")
	}
	nodes := s.subscription.Nodes

	// try as 1-based index
	if idx, err := strconv.Atoi(nameOrIndex); err == nil {
		if idx < 1 || idx > len(nodes) {
			return fmt.Errorf("index %d out of range (1-%d)", idx, len(nodes))
		}
		node := &nodes[idx-1]
		if !node.Supported {
			return fmt.Errorf("node %q (type %s) is not supported", node.Name, node.Type)
		}
		s.activeNode = node
		s.activeURL = node.URL.String()
		s.autoURL = ""
		s.autoDial = nil
		s.singleDial = nil
		s.publishChainLocked()
		return nil
	}

	// try as name (case-insensitive)
	lower := strings.ToLower(nameOrIndex)
	for i := range nodes {
		if strings.ToLower(nodes[i].Name) == lower {
			if !nodes[i].Supported {
				return fmt.Errorf("node %q (type %s) is not supported", nodes[i].Name, nodes[i].Type)
			}
			s.activeNode = &nodes[i]
			s.activeURL = nodes[i].URL.String()
			s.autoURL = ""
			s.autoDial = nil
			s.singleDial = nil
			s.publishChainLocked()
			return nil
		}
	}
	return fmt.Errorf("node %q not found", nameOrIndex)
}

func (s *State) SetAutoDial(clashURL string, dial proxyclient.Dial) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoURL = clashURL
	s.autoDial = dial
	s.singleDial = nil
	s.activeNode = nil
	s.activeURL = ""
	s.publishChainLocked()
}

// SetProxyURL routes egress through a single persistent proxy URL (socks5://,
// trojan://, …). Unlike WithOverrideDial it is not scoped to one command; it
// stays the active egress until changed or cleared.
func (s *State) SetProxyURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid proxy URL: %w", err)
	}
	d, err := proxyclient.NewClient(u)
	if err != nil {
		return fmt.Errorf("create proxy client: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.singleDial = d
	s.activeURL = rawURL
	s.activeNode = nil
	s.autoURL = ""
	s.autoDial = nil
	s.publishChainLocked()
	return nil
}

func (s *State) ActiveProxy() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.autoURL != "" {
		return s.autoURL
	}
	if s.activeURL != "" {
		return s.activeURL
	}
	return s.originalProxy
}

func (s *State) IsAutoMode() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.autoURL != ""
}

func (s *State) ActiveNodeName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.activeNode != nil {
		return s.activeNode.Name
	}
	return ""
}

func (s *State) OriginalProxy() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.originalProxy
}

func (s *State) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscription = nil
	s.subscribeURL = ""
	s.activeNode = nil
	s.activeURL = ""
	s.autoURL = ""
	s.autoDial = nil
	s.singleDial = nil
	s.publishChainLocked()
}

func (s *State) TestNode(ctx context.Context, node *clash.ProxyNode) (time.Duration, error) {
	if node == nil || node.URL == nil {
		return 0, fmt.Errorf("invalid node")
	}
	dial, err := proxyclient.NewClient(node.URL)
	if err != nil {
		return 0, fmt.Errorf("dial setup: %w", err)
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dial.DialContext(ctx, network, addr)
		},
		TLSClientConfig:   &tls.Config{},
		DisableKeepAlives: true,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	start := time.Now()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://www.google.com/generate_204", nil)
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return latency, err
	}
	_, _ = io.ReadAll(io.LimitReader(resp.Body, 64))
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return latency, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return latency, nil
}
