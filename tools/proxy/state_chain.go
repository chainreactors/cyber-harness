package proxy

import (
	"fmt"
	"net/url"

	"github.com/chainreactors/proxyclient"
)

func (s *State) CurrentDial() proxyclient.Dial {
	if pointer := s.chain.Load(); pointer != nil && *pointer != nil {
		return *pointer
	}
	return proxyclient.DefaultDial
}

func (s *State) WithOverrideDial(proxyURL string) (func(), error) {
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	dial, err := proxyclient.NewClient(parsed)
	if err != nil {
		return nil, fmt.Errorf("create proxy client: %w", err)
	}
	previous := s.chain.Load()
	current := proxyclient.Dial(dial)
	s.chain.Store(&current)
	return func() { s.chain.Store(previous) }, nil
}

func (s *State) publishChainLocked() {
	var dial proxyclient.Dial
	switch {
	case s.autoDial != nil:
		dial = s.autoDial
	case s.singleDial != nil:
		dial = s.singleDial
	case s.activeNode != nil && s.activeNode.URL != nil:
		if client, err := proxyclient.NewClient(s.activeNode.URL); err == nil {
			dial = client
		}
	case s.originalProxy != "":
		if parsed, err := url.Parse(s.originalProxy); err == nil {
			if client, err := proxyclient.NewClient(parsed); err == nil {
				dial = client
			}
		}
	}
	if dial == nil {
		dial = proxyclient.DefaultDial
	}
	s.chain.Store(&dial)
}
