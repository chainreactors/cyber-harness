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
	var ev monitorEvent
	if json.Unmarshal([]byte(line), &ev) != nil {
		return
	}
	m.renderEvent(ev)
}

type monitorEvent struct {
	Type     string       `json:"type"`
	Turn     int          `json:"turn"`
	ToolName string       `json:"tool_name"`
	Args     string       `json:"arguments"`
	Result   string       `json:"result"`
	IsError  bool         `json:"is_error"`
	Message  *monitorMsg  `json:"message"`
	Stop     string       `json:"stop"`
}

type monitorMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (m *Monitor) renderEvent(ev monitorEvent) {
	switch ev.Type {
	case "turn_start":
		if ev.Turn != m.turnSeen {
			m.turnSeen = ev.Turn
			m.printf("\n── turn %d ──\n", ev.Turn)
		}

	case "message_end":
		if ev.Message != nil && ev.Message.Role == "assistant" && ev.Message.Content != "" {
			text := ev.Message.Content
			if len(text) > 200 {
				text = text[:197] + "..."
			}
			m.printf("  💬 %s\n", text)
		}

	case "tool_execution_start":
		args := ev.Args
		if len(args) > 120 {
			args = args[:117] + "..."
		}
		m.printf("  🔧 %s %s\n", ev.ToolName, args)

	case "tool_execution_end":
		if ev.IsError {
			result := ev.Result
			if len(result) > 100 {
				result = result[:97] + "..."
			}
			m.printf("  ❌ %s error: %s\n", ev.ToolName, result)
		} else {
			size := len(ev.Result)
			preview := ev.Result
			if len(preview) > 100 {
				preview = preview[:97] + "..."
			}
			if size > 0 {
				m.printf("  ✓  %s → %d bytes: %s\n", ev.ToolName, size, preview)
			} else {
				m.printf("  ✓  %s → (empty)\n", ev.ToolName)
			}
		}

	case "agent_end":
		m.printf("\n── agent done (stop=%s) ──\n", ev.Stop)
	}
}
