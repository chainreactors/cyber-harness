package agent

import (
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
)

func TestWebNodeRefUsesWebIdentity(t *testing.T) {
	ref, err := webNodeRef(&cfg.Option{
		AgentOptions: cfg.AgentOptions{ServerURL: "https://secret@example.test/hub"},
		IOAOptions:   cfg.IOAOptions{IOANodeName: "worker-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID != "worker-1" || ref.Authority != "https://example.test/hub" {
		t.Fatalf("node ref = %#v", ref)
	}
	if _, err := webNodeRef(&cfg.Option{AgentOptions: cfg.AgentOptions{ServerURL: "https://example.test"}}); err == nil {
		t.Fatal("expected missing ioa.node_name error")
	}
}
