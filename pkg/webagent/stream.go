package webagent

import (
	"bytes"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

// MaxStreamBuf is the maximum buffer size before a StreamWriter flushes.
const MaxStreamBuf = 64 << 10

// StreamWriter buffers tool output and sends it line-by-line over the WebSocket.
type StreamWriter struct {
	TaskID string
	SendFn func(webproto.Message)
	// Stream optionally tags output messages with an ExecStreamPayload
	// (webproto.StreamStdout/StreamStderr) for consumers that split streams.
	Stream string
	buf    []byte
}

func (w *StreamWriter) send(data string) {
	msg := webproto.Message{Type: "output", TaskID: w.TaskID, Data: data}
	if w.Stream != "" {
		msg.Payload = webproto.MustJSON(webproto.ExecStreamPayload{Stream: w.Stream})
	}
	w.SendFn(msg)
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
		w.send(line)
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
		w.send(data)
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
