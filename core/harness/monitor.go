//go:build e2e

package harness

import (
	"fmt"
	"io"

	"github.com/chainreactors/aiscan/pkg/agent/truncate"
	"github.com/chainreactors/aiscan/pkg/aop"
)

// Monitor renders the AOP events received from the agent's webproto stdout.
// Attach it to a Harness with h.WithMonitor().
//
// Output goes to the provided Writer (typically os.Stderr for live
// terminal view, or a test log adapter).
type Monitor struct {
	out     io.Writer
	runSeen string
}

func NewMonitor(out io.Writer) *Monitor {
	return &Monitor{out: out}
}

func (m *Monitor) printf(format string, args ...any) {
	fmt.Fprintf(m.out, format, args...)
}

func (m *Monitor) renderEvent(ev aop.Event) {
	switch ev.Type {
	case aop.TypeSessionStart:
		m.runSeen = ""

	case aop.TypeTurnStart:
		if ev.TurnID != "" && ev.TurnID != m.runSeen {
			m.runSeen = ev.TurnID
			m.printf("\n── run %s ──\n", ev.TurnID)
		}

	case aop.TypeMessage:
		data, err := aop.DecodeData[aop.MessageData](ev)
		if err == nil && (data.Role == "" || data.Role == "assistant") {
			if text := messageText(data); text != "" {
				m.printf("  💬 %s\n", truncate.Clip(text, 200))
			}
		}

	case aop.TypeToolCall:
		data, err := aop.DecodeData[aop.ToolCallData](ev)
		if err == nil {
			m.printf("  🔧 %s %s\n", data.ToolName, truncate.Clip(argsText(data.Args), 120))
		}

	case aop.TypeToolResult:
		data, err := aop.DecodeData[aop.ToolResultData](ev)
		if err == nil {
			result := valueText(data.Content)
			switch {
			case data.IsError:
				m.printf("  ❌ %s error: %s\n", data.ToolName, truncate.Clip(result, 100))
			case result != "":
				m.printf("  ✓  %s → %d bytes: %s\n", data.ToolName, len(result), truncate.Clip(result, 100))
			default:
				m.printf("  ✓  %s → (empty)\n", data.ToolName)
			}
		}

	case aop.TypeTurnEnd:
		data, err := aop.DecodeData[aop.TurnEndData](ev)
		if err == nil {
			m.printf("\n── run done (stop=%s) ──\n", data.Stop)
		}
	}
}
