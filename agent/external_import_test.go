package agent_test

import (
	"testing"

	"github.com/chainreactors/aiscan/agent"
)

func TestRootAgentPublicImport(t *testing.T) {
	config := agent.Config{}.
		WithModel("example-model").
		WithMaxTokens(256).
		WithContextWindow(4096)
	if config.Model != "example-model" || config.MaxTokens != 256 || config.ContextWindow != 4096 {
		t.Fatalf("root agent config aliases/builders are not externally usable: %#v", config)
	}
	_ = agent.ProviderConfig{Model: "example-model"}
}
