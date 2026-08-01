package agent

import (
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"google.golang.org/protobuf/proto"
)

// AgentStatsTracker tracks agent event statistics for the WebSocket connection.
type AgentStatsTracker struct {
	mu    sync.Mutex
	stats transport.AgentStats
}

// NewAgentStatsTracker creates a new stats tracker.
func NewAgentStatsTracker() *AgentStatsTracker {
	return &AgentStatsTracker{}
}

// Snapshot returns the current stats snapshot.
func (t *AgentStatsTracker) Snapshot() *transport.AgentStats {
	if t == nil {
		return &transport.AgentStats{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return proto.Clone(&t.stats).(*transport.AgentStats)
}

// Observe records an AOP event and returns updated stats if the stats changed.
func (t *AgentStatsTracker) Observe(e *aop.Event) (*transport.AgentStats, bool) {
	if t == nil {
		return &transport.AgentStats{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stats.LastEvent = aop.Kind(e)
	switch payload := e.Payload.(type) {
	case *aop.Event_TurnStarted:
		t.stats.Turns++
	case *aop.Event_Usage:
		data := payload.Usage
		t.stats.InputTokens += data.InputTokens
		t.stats.OutputTokens += data.OutputTokens
		t.stats.TotalTokens += data.TotalTokens
		t.stats.CacheReadTokens += data.Detail["cache_read"]
		t.stats.CacheWriteTokens += data.Detail["cache_write"]
	case *aop.Event_ToolCall:
		t.stats.ToolCalls++
		t.stats.RunningTools++
	case *aop.Event_ToolResult:
		if t.stats.RunningTools > 0 {
			t.stats.RunningTools--
		}
	default:
		return proto.Clone(&t.stats).(*transport.AgentStats), false
	}
	return proto.Clone(&t.stats).(*transport.AgentStats), true
}
