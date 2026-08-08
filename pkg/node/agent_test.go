package node

import (
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
)

func TestWebNodeID(t *testing.T) {
	nodeID, err := webNodeID(&cfg.Option{IOAOptions: cfg.IOAOptions{IOANodeName: "worker-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if nodeID != "worker-1" {
		t.Fatalf("node_id = %q", nodeID)
	}
	nodeID, err = webNodeID(&cfg.Option{IOAOptions: cfg.IOAOptions{IOANodeID: "existing-1", IOANodeName: "worker-1"}})
	if err != nil || nodeID != "existing-1" {
		t.Fatalf("existing node_id = %q, err = %v", nodeID, err)
	}
	if _, err := webNodeID(&cfg.Option{}); err == nil {
		t.Fatal("expected missing node_id error")
	}
}

func TestResolveRemoteAgentURLsDerivesEmbeddedIOA(t *testing.T) {
	option := &cfg.Option{
		AgentOptions: cfg.AgentOptions{ServerURL: "http://token@127.0.0.1:18080"},
	}
	if err := resolveRemoteAgentURLs(option); err != nil {
		t.Fatal(err)
	}
	if option.ServerURL != "http://token@127.0.0.1:18080" {
		t.Fatalf("server URL = %q", option.ServerURL)
	}
	if option.IOAURL != "http://token@127.0.0.1:18080/ioa" {
		t.Fatalf("IOA URL = %q, want same-origin embedded endpoint", option.IOAURL)
	}
}

func TestResolveRemoteAgentURLsPreservesIndependentIOA(t *testing.T) {
	option := &cfg.Option{
		AgentOptions: cfg.AgentOptions{ServerURL: "http://token@127.0.0.1:18080"},
		IOAOptions:   cfg.IOAOptions{IOAURL: "http://ioa-token@127.0.0.1:18765"},
	}
	if err := resolveRemoteAgentURLs(option); err != nil {
		t.Fatal(err)
	}
	if option.IOAURL != "http://ioa-token@127.0.0.1:18765" {
		t.Fatalf("independent IOA URL = %q", option.IOAURL)
	}
}
