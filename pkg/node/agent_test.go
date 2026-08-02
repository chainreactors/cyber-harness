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
