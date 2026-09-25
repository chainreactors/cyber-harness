package proxy

import (
	"net/url"

	"github.com/chainreactors/proxyclient"
)

func (s *State) CurrentDial() proxyclient.Dial {
	if pointer := s.chain.Load(); pointer != nil && *pointer != nil {
		return *pointer
	}
	return proxyclient.DefaultDial
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
