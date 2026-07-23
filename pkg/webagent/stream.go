package webagent

import (
	"sync"

	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

// AgentStatsTracker tracks agent event statistics for the WebSocket connection.
type AgentStatsTracker struct {
	mu    sync.Mutex
	stats webproto.AgentStats
}

// NewAgentStatsTracker creates a new stats tracker.
func NewAgentStatsTracker() *AgentStatsTracker {
	return &AgentStatsTracker{}
}

// Snapshot returns the current stats snapshot.
func (t *AgentStatsTracker) Snapshot() webproto.AgentStats {
	if t == nil {
		return webproto.AgentStats{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats
}

// Observe records an AOP event and returns updated stats if the stats changed.
func (t *AgentStatsTracker) Observe(e aop.Event) (webproto.AgentStats, bool) {
	if t == nil {
		return webproto.AgentStats{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stats.LastEvent = e.Type
	switch e.Type {
	case aop.TypeTurnEnd:
		if data, err := aop.DecodeData[aop.TurnEndData](e); err == nil && data.Turn > t.stats.Turns {
			t.stats.Turns = data.Turn
		}
	case aop.TypeUsage:
		data, err := aop.DecodeData[aop.UsageData](e)
		if err != nil {
			return t.stats, false
		}
		t.stats.PromptTokens += data.InputTokens
		t.stats.CompletionTokens += data.OutputTokens
		t.stats.TotalTokens += data.TotalTokens
		t.stats.CacheReadTokens += data.CacheReadTokens
		t.stats.CacheWriteTokens += data.CacheWriteTokens
	case aop.TypeToolCall:
		t.stats.ToolCalls++
		t.stats.RunningTools++
	case aop.TypeToolResult:
		if t.stats.RunningTools > 0 {
			t.stats.RunningTools--
		}
	default:
		return t.stats, false
	}
	return t.stats, true
}
