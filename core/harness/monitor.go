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
	out      io.Writer
	turnSeen int
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
		m.turnSeen = 0

	case aop.TypeTurnStart:
		data, err := aop.DecodeData[aop.TurnData](ev)
		if err == nil && data.Turn != m.turnSeen {
			m.turnSeen = data.Turn
			m.printf("\n── turn %d ──\n", data.Turn)
		}

	case aop.TypeText:
		data, err := aop.DecodeData[aop.TextData](ev)
		if err == nil && !data.Delta && data.Content != "" && (data.Role == "" || data.Role == "assistant") {
			m.printf("  💬 %s\n", truncate.Clip(data.Content, 200))
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

	case aop.TypeSessionEnd:
		data, err := aop.DecodeData[aop.SessionEndData](ev)
		if err == nil {
			m.printf("\n── agent done (stop=%s) ──\n", data.Stop)
		}
	}
}
