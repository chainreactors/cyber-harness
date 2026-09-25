package node

import (
	"github.com/chainreactors/cyber/core/telemetry"
	cfg "github.com/chainreactors/cyber/pkg/config"
	profilepkg "github.com/chainreactors/cyber/pkg/profile"
	"testing"
)

func TestRunRemoteAgentRejectsNilConstructorAndResult(t *testing.T) {
	option := &cfg.Option{
		AgentOptions: cfg.AgentOptions{ServerURL: "http://127.0.0.1:18080"},
		NodeOptions:  cfg.NodeOptions{NodeID: "worker-1"},
	}
	if err := RunWebSocket(t.Context(), nil, option, telemetry.NopLogger()); err == nil {
		t.Fatal("nil profile constructor was accepted")
	}
	newProfile := func(profilepkg.Request) (profilepkg.Profile, error) { return nil, nil }
	if err := RunWebSocket(t.Context(), newProfile, option, telemetry.NopLogger()); err == nil {
		t.Fatal("nil profile result was accepted")
	}
}

func TestWebNodeID(t *testing.T) {
	nodeID, err := webNodeID(&cfg.Option{NodeOptions: cfg.NodeOptions{NodeName: "worker-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if nodeID != "worker-1" {
		t.Fatalf("node_id = %q", nodeID)
	}
	nodeID, err = webNodeID(&cfg.Option{NodeOptions: cfg.NodeOptions{NodeID: "existing-1", NodeName: "worker-1"}})
	if err != nil || nodeID != "existing-1" {
		t.Fatalf("existing node_id = %q, err = %v", nodeID, err)
	}
	if _, err := webNodeID(&cfg.Option{}); err == nil {
		t.Fatal("expected missing node_id error")
	}
}
