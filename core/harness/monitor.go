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

func (m *Monitor) renderEvent(ev AgentEvent) {
	switch ev.AOPType {
	case aop.TypeSessionStart:
		m.turnSeen = 0

	case aop.TypeTurnStart:
		if ev.Turn != m.turnSeen {
			m.turnSeen = ev.Turn
			m.printf("\n── turn %d ──\n", ev.Turn)
		}

	case aop.TypeText:
		if !ev.Delta && ev.Content != "" && (ev.Role == "" || ev.Role == "assistant") {
			m.printf("  💬 %s\n", truncate.Clip(ev.Content, 200))
		}

	case aop.TypeToolCall:
		m.printf("  🔧 %s %s\n", ev.ToolName, truncate.Clip(ev.ArgsText(), 120))

	case aop.TypeToolResult:
		result := ev.ResultText()
		if ev.IsError {
			m.printf("  ❌ %s error: %s\n", ev.ToolName, truncate.Clip(result, 100))
		} else if result != "" {
			m.printf("  ✓  %s → %d bytes: %s\n", ev.ToolName, len(result), truncate.Clip(result, 100))
		} else {
			m.printf("  ✓  %s → (empty)\n", ev.ToolName)
		}

	case aop.TypeSessionEnd:
		m.printf("\n── agent done (stop=%s) ──\n", ev.Stop)
	}
}
