package proxy

import (
	"net/url"
	"testing"

	"github.com/chainreactors/proxyclient/extra/clash"
)

func TestNewState(t *testing.T) {
	s := NewState("socks5://127.0.0.1:1080")
	if s.OriginalProxy() != "socks5://127.0.0.1:1080" {
		t.Fatalf("OriginalProxy() = %q", s.OriginalProxy())
	}
	if s.ActiveProxy() != "socks5://127.0.0.1:1080" {
		t.Fatalf("ActiveProxy() = %q, want original", s.ActiveProxy())
	}
	if s.IsAutoMode() {
		t.Fatal("should not be in auto mode initially")
	}
	if s.ActiveNodeName() != "" {
		t.Fatalf("ActiveNodeName() = %q, want empty", s.ActiveNodeName())
	}
}

func TestNewStateEmpty(t *testing.T) {
	s := NewState("")
	if s.ActiveProxy() != "" {
		t.Fatalf("ActiveProxy() = %q, want empty", s.ActiveProxy())
	}
}

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func makeTestSubscription() *clash.Subscription {
	return &clash.Subscription{
		Nodes: []clash.ProxyNode{
			{Name: "HK-Node", Type: "trojan", Server: "hk.example.com", Port: 443, Supported: true, URL: mustParseURL("trojan://pass@hk.example.com:443")},
			{Name: "US-Node", Type: "vless", Server: "us.example.com", Port: 443, Supported: true, URL: mustParseURL("vless://id@us.example.com:443")},
			{Name: "Unsupported", Type: "unknown", Server: "x.example.com", Port: 443, Supported: false},
		},
	}
}

func TestLoadSubscription(t *testing.T) {
	s := NewState("")
	sub := makeTestSubscription()
	s.LoadSubscription(sub, "https://sub.example.com")

	nodes := s.Nodes()
	if len(nodes) != 3 {
		t.Fatalf("Nodes() len = %d, want 3", len(nodes))
	}
}

func TestSwitchByIndex(t *testing.T) {
	s := NewState("")
	s.LoadSubscription(makeTestSubscription(), "")

	if err := s.Switch("1"); err != nil {
		t.Fatalf("Switch(1) error = %v", err)
	}
	if s.ActiveNodeName() != "HK-Node" {
		t.Fatalf("ActiveNodeName() = %q, want HK-Node", s.ActiveNodeName())
	}

	if err := s.Switch("2"); err != nil {
		t.Fatalf("Switch(2) error = %v", err)
	}
	if s.ActiveNodeName() != "US-Node" {
		t.Fatalf("ActiveNodeName() = %q, want US-Node", s.ActiveNodeName())
	}
}

func TestSwitchByName(t *testing.T) {
	s := NewState("")
	s.LoadSubscription(makeTestSubscription(), "")

	if err := s.Switch("hk-node"); err != nil {
		t.Fatalf("Switch(hk-node) error = %v", err)
	}
	if s.ActiveNodeName() != "HK-Node" {
		t.Fatalf("ActiveNodeName() = %q, want HK-Node", s.ActiveNodeName())
	}
}

func TestSwitchIndexOutOfRange(t *testing.T) {
	s := NewState("")
	s.LoadSubscription(makeTestSubscription(), "")

	if err := s.Switch("0"); err == nil {
		t.Fatal("expected error for index 0")
	}
	if err := s.Switch("99"); err == nil {
		t.Fatal("expected error for index 99")
	}
}

func TestSwitchUnsupportedNode(t *testing.T) {
	s := NewState("")
	s.LoadSubscription(makeTestSubscription(), "")

	err := s.Switch("3")
	if err == nil {
		t.Fatal("expected error for unsupported node")
	}
	if got := err.Error(); !contains(got, "not supported") {
		t.Fatalf("error = %q, want 'not supported'", got)
	}
}

func TestSwitchUnknownName(t *testing.T) {
	s := NewState("")
	s.LoadSubscription(makeTestSubscription(), "")

	err := s.Switch("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown name")
	}
	if got := err.Error(); !contains(got, "not found") {
		t.Fatalf("error = %q, want 'not found'", got)
	}
}

func TestStateSwitchNoSubscription(t *testing.T) {
	s := NewState("")
	if err := s.Switch("1"); err == nil {
		t.Fatal("expected error without subscription")
	}
}

func TestClearResetsState(t *testing.T) {
	s := NewState("original://proxy")
	s.LoadSubscription(makeTestSubscription(), "https://sub.example.com")
	s.Switch("1")

	s.Clear()

	if nodes := s.Nodes(); nodes != nil {
		t.Fatalf("Nodes() = %v after Clear, want nil", nodes)
	}
	if s.ActiveNodeName() != "" {
		t.Fatalf("ActiveNodeName() = %q after Clear, want empty", s.ActiveNodeName())
	}
	if s.IsAutoMode() {
		t.Fatal("IsAutoMode() = true after Clear")
	}
	if s.ActiveProxy() != "original://proxy" {
		t.Fatalf("ActiveProxy() = %q after Clear, want original", s.ActiveProxy())
	}
}

func TestSetAutoDial(t *testing.T) {
	s := NewState("")
	s.SetAutoDial("clash://auto", nil)

	if !s.IsAutoMode() {
		t.Fatal("IsAutoMode() = false after SetAutoDial")
	}
	if s.ActiveProxy() != "clash://auto" {
		t.Fatalf("ActiveProxy() = %q, want clash://auto", s.ActiveProxy())
	}
}

func TestActiveProxyPriority(t *testing.T) {
	s := NewState("original://proxy")

	// original proxy is the default
	if s.ActiveProxy() != "original://proxy" {
		t.Fatalf("ActiveProxy() = %q, want original", s.ActiveProxy())
	}

	// switch to a node → activeURL takes priority
	s.LoadSubscription(makeTestSubscription(), "")
	s.Switch("1")
	active := s.ActiveProxy()
	if active == "original://proxy" || active == "" {
		t.Fatalf("ActiveProxy() after Switch = %q, want node URL", active)
	}

	// auto mode takes highest priority
	s.SetAutoDial("clash://auto", nil)
	if s.ActiveProxy() != "clash://auto" {
		t.Fatalf("ActiveProxy() after SetAutoDial = %q, want clash://auto", s.ActiveProxy())
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
