package proxy

import (
	"testing"

	"github.com/chainreactors/proxyclient"
)

// dialPtr returns the identity of the currently published chain pointer so a
// swap can be observed without comparing func values (which are not comparable).
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

func TestWithOverrideDialSwapAndRestore(t *testing.T) {
	s := NewState("")
	base := s.dialPtr()

	restore, err := s.WithOverrideDial("socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("override failed: %v", err)
	}
	if s.dialPtr() == base {
		t.Fatal("override should republish a different chain pointer")
	}

	restore()
	if s.dialPtr() != base {
		t.Fatal("restore should return to the previous chain pointer")
	}
}

func TestWithOverrideDialRejectsBadURL(t *testing.T) {
	s := NewState("")
	if _, err := s.WithOverrideDial("://not a url"); err == nil {
		t.Fatal("expected error for malformed proxy URL")
	}
}
