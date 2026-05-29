package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
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
	autoURL       string               // clash:// URL for auto mode
	autoDial      proxyclient.Dial     // pre-built dial for auto mode
}

func NewState(originalProxy string) *State {
	return &State{originalProxy: originalProxy}
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
	s.activeNode = nil
	s.activeURL = ""
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
		TLSClientConfig:  &tls.Config{},
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
