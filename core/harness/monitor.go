//go:build e2e

package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/truncate"
	"github.com/chainreactors/aiscan/pkg/aop"
)

// Monitor tails the agent events JSONL file in real-time, rendering a
// compact live view of what the agent is doing. Attach it to a Harness
// with h.WithMonitor(). The monitor runs in a background goroutine and
// stops automatically when the agent process exits.
//
// Output goes to the provided Writer (typically os.Stderr for live
// terminal view, or a test log adapter).
type Monitor struct {
	out      io.Writer
	mu       sync.Mutex
	stopped  bool
	turnSeen int
}

func NewMonitor(out io.Writer) *Monitor {
	return &Monitor{out: out}
}

func (m *Monitor) printf(format string, args ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fmt.Fprintf(m.out, format, args...)
}

// run tails the events file until stop is called.
func (m *Monitor) run(path string, done <-chan struct{}) {
	for {
		f, err := os.Open(path)
		if err == nil {
			m.tailFile(f, done)
			f.Close()
			return
		}
		select {
		case <-done:
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (m *Monitor) tailFile(f *os.File, done <-chan struct{}) {
	var offset int64
	buf := make([]byte, 64*1024)
	var partial string

	for {
		n, _ := f.ReadAt(buf, offset)
		if n > 0 {
			offset += int64(n)
			data := partial + string(buf[:n])
			partial = ""

			lines := strings.Split(data, "\n")
			for i, line := range lines {
				if i == len(lines)-1 && !strings.HasSuffix(data, "\n") {
					partial = line
					continue
				}
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				m.renderLine(line)
			}
		}

		select {
		case <-done:
			if partial != "" {
				m.renderLine(strings.TrimSpace(partial))
			}
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (m *Monitor) renderLine(line string) {
	var ev aop.Event
	if json.Unmarshal([]byte(line), &ev) != nil || ev.V == 0 {
		return
	}
	m.renderAOPEvent(ev)
}

func (m *Monitor) renderAOPEvent(ev aop.Event) {
	switch ev.Type {
	case aop.TypeTurnStart:
		var d aop.TurnData
		_ = json.Unmarshal(ev.Data, &d)
		if d.Turn != m.turnSeen {
			m.turnSeen = d.Turn
			m.printf("\n── turn %d ──\n", d.Turn)
		}

	case aop.TypeText:
		var d aop.TextData
		_ = json.Unmarshal(ev.Data, &d)
		if !d.Delta && d.Content != "" && (d.Role == "" || d.Role == "assistant") {
			m.printf("  💬 %s\n", truncate.Clip(d.Content, 200))
		}

	case aop.TypeToolCall:
		var d aop.ToolCallData
		_ = json.Unmarshal(ev.Data, &d)
		argsStr := ""
		if s, ok := d.Args.(string); ok {
			argsStr = s
		} else if d.Args != nil {
			raw, _ := json.Marshal(d.Args)
			argsStr = string(raw)
		}
		m.printf("  🔧 %s %s\n", d.ToolName, truncate.Clip(argsStr, 120))

	case aop.TypeToolResult:
		var d aop.ToolResultData
		_ = json.Unmarshal(ev.Data, &d)
		result := ""
		if s, ok := d.Content.(string); ok {
			result = s
		}
		if d.IsError {
			m.printf("  ❌ %s error: %s\n", d.ToolName, truncate.Clip(result, 100))
		} else if len(result) > 0 {
			m.printf("  ✓  %s → %d bytes: %s\n", d.ToolName, len(result), truncate.Clip(result, 100))
		} else {
			m.printf("  ✓  %s → (empty)\n", d.ToolName)
		}

	case aop.TypeSessionEnd:
		var d aop.SessionEndData
		_ = json.Unmarshal(ev.Data, &d)
		m.printf("\n── agent done (stop=%s) ──\n", d.Stop)
	}
}
