package terminal

import (
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/utils/proc"
)

// FormatCompletion renders a finished background session for the agent's inbox.
// This is product-facing prose, so it lives here rather than in the registry:
// proc knows when a unit ended, not how to describe it to a model.
func FormatCompletion(info proc.Info, lastOutput string) string {
	duration := info.EndedAt.Sub(info.StartedAt).Round(time.Second)
	status := "completed"
	switch {
	case info.State == proc.StateKilled:
		status = "killed"
		if info.Reason != "" {
			status += " (" + info.Reason + ")"
		}
	case info.State == proc.StateFailed && info.Reason != "":
		status = "failed (" + info.Reason + ")"
	case info.ExitStatus() != 0:
		status = fmt.Sprintf("exited with code %d", info.ExitStatus())
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "<session_completion id=%q name=%q exit_code=%d duration=%q>\n",
		info.ID, info.Name, info.ExitStatus(), duration.String())
	fmt.Fprintf(&sb, "Background session %s.\n", status)
	if lastOutput != "" {
		sb.WriteString("--- last 20 lines ---\n")
		sb.WriteString(lastOutput)
		sb.WriteString("\n")
	} else {
		sb.WriteString("(no output)\n")
	}
	sb.WriteString("</session_completion>")
	return sb.String()
}
