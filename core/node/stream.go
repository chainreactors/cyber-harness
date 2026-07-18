package node

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

// MaxStreamBuf is the maximum buffer size before a StreamWriter flushes.
const MaxStreamBuf = 64 << 10

// StreamWriter buffers tool output and sends it line-by-line over the WebSocket.
type StreamWriter struct {
	TaskID string
	SendFn func(webproto.Message)
	buf    []byte
}

func (w *StreamWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			if len(w.buf) >= MaxStreamBuf {
				w.Flush()
			}
			break
		}
		line := string(w.buf[:idx])
		w.buf = w.buf[idx+1:]
		if strings.TrimSpace(line) == "" {
			continue
		}
		w.SendFn(webproto.Message{Type: "output", TaskID: w.TaskID, Data: line})
	}
	return len(p), nil
}

// Flush sends any remaining buffered data.
func (w *StreamWriter) Flush() {
	if len(w.buf) == 0 {
		return
	}
	data := string(w.buf)
	w.buf = w.buf[:0]
	if strings.TrimSpace(data) != "" {
		w.SendFn(webproto.Message{Type: "output", TaskID: w.TaskID, Data: data})
	}
}

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

// Observe records an agent event and returns updated stats if the stats changed.
func (t *AgentStatsTracker) Observe(e agent.Event) (webproto.AgentStats, bool) {
	if t == nil {
		return webproto.AgentStats{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stats.LastEvent = string(e.Type)
	switch e.Type {
	case agent.EventTurnEnd:
		if e.Turn > t.stats.Turns {
			t.stats.Turns = e.Turn
		}
		if e.Usage != nil {
			t.stats.PromptTokens += e.Usage.PromptTokens
			t.stats.CompletionTokens += e.Usage.CompletionTokens
			t.stats.TotalTokens += e.Usage.TotalTokens
			t.stats.CacheReadTokens += e.Usage.CacheReadTokens
			t.stats.CacheWriteTokens += e.Usage.CacheWriteTokens
		}
	case agent.EventToolExecutionStart:
		t.stats.ToolCalls++
		t.stats.RunningTools++
	case agent.EventToolExecutionEnd:
		if t.stats.RunningTools > 0 {
			t.stats.RunningTools--
		}
	default:
		return t.stats, false
	}
	return t.stats, true
}

// AgentEventSummary returns a human-readable summary for an agent event.
func AgentEventSummary(e agent.Event) string {
	switch e.Type {
	case agent.EventToolExecutionStart:
		return e.ToolName
	case agent.EventToolExecutionEnd:
		if e.IsError {
			return e.ToolName + " error"
		}
		return e.ToolName + " done"
	case agent.EventTurnStart:
		return fmt.Sprintf("turn %d", e.Turn)
	case agent.EventTurnEnd:
		if e.Usage != nil {
			return fmt.Sprintf("turn %d tokens=%d", e.Turn, e.Usage.TotalTokens)
		}
		return fmt.Sprintf("turn %d", e.Turn)
	default:
		return ""
	}
}
