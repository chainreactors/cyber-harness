package proxy

import (
	"testing"

	"github.com/chainreactors/proxyclient"
)

// dialPtr identifies the published chain without comparing func values.
func (s *State) dialPtr() *proxyclient.Dial { return s.chain.Load() }

func TestCurrentDialNeverNil(t *testing.T) {
	s := NewState("")
	if s.CurrentDial() == nil {
		t.Fatal("CurrentDial must never return nil, even with no selection")
	}
	if s.dialPtr() == nil {
		t.Fatal("NewState must publish an initial chain")
	}
}

func TestOriginalProxyBecomesChain(t *testing.T) {
	s := NewState("socks5://127.0.0.1:1080")
	if s.CurrentDial() == nil {
		t.Fatal("original proxy should yield a dial")
	}
}

func TestSetProxyURLPublishesPersistentRoute(t *testing.T) {
	s := NewState("")
	base := s.dialPtr()

	if err := s.SetProxyURL("socks5://127.0.0.1:1080"); err != nil {
		t.Fatalf("set proxy failed: %v", err)
	}
	if s.dialPtr() == base {
		t.Fatal("route should publish a different chain pointer")
	}
	if s.ActiveProxy() != "socks5://127.0.0.1:1080" {
		t.Fatalf("active proxy = %q", s.ActiveProxy())
	}
}

func TestSetProxyURLRejectsBadURL(t *testing.T) {
	s := NewState("")
	if err := s.SetProxyURL("://not a url"); err == nil {
		t.Fatal("expected error for malformed proxy URL")
	}
}
