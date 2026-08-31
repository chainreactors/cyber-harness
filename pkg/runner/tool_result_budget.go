package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/tool"
)

// Tool results feed the model context, so their budget is set by what the model
// can usefully absorb, not by what the transport can carry. Artifact payloads are
// already bounded separately (see tools/toolargs) for the transport; the inline
// ToolResult channel had no budget at all, so one large scan result could be
// marshaled as a single WebSocket message big enough to trip the control plane's
// read limit and sever the connection for every in-flight call.
//
// The budget sits at the point where more raw bytes stop helping the model reason
// (~tens of KB): past it, a bounded preview plus an on-disk reference beats a full
// dump. It is deliberately not scaled by context-window size — a larger window
// holds more results, it does not make any single result more absorbable.
const (
	// toolResultBudgetBytes caps the inline text of one tool result. 64 KiB is
	// ~16-20k tokens, ~2% of a 1M context window.
	toolResultBudgetBytes = 64 << 10
	// toolResultTailBytes keeps the conclusion at the bottom of the output.
	toolResultTailBytes = 16 << 10
	// toolResultMarkerReserve leaves room for the separator and the reference
	// marker so the assembled preview never exceeds the budget.
	toolResultMarkerReserve = 2 << 10
	// toolResultHeadBytes keeps the structure at the top; head + tail + reserve
	// equals the budget.
	toolResultHeadBytes = toolResultBudgetBytes - toolResultTailBytes - toolResultMarkerReserve
	// spillDirName is the workspace-relative directory the full output is written
	// to, so the read tool can page it back with offset/limit.
	spillDirName = ".cairn/spill"
)

// boundToolResultOutput collapses an over-budget result to a head+tail preview
// plus an actionable reference to the spilled full output. Results within budget
// are returned unchanged.
func boundToolResultOutput(ctx context.Context, result *aop.ToolResult) {
	if result == nil {
		return
	}
	total := 0
	for _, content := range result.Output {
		if text := content.GetText(); text != nil {
			total += len(text.Text)
		}
	}
	if total <= toolResultBudgetBytes {
		return
	}

	var sb strings.Builder
	sb.Grow(total)
	for _, content := range result.Output {
		if text := content.GetText(); text != nil {
			sb.WriteString(text.Text)
		}
	}
	full := sb.String()

	workDir := tool.WorkDirFromContext(ctx, "")
	ref, spilled := spillToolResultOutput(workDir, result.CallId, full)

	var preview strings.Builder
	preview.WriteString(headBytes(full, toolResultHeadBytes))
	preview.WriteString("\n\n...[output truncated]...\n\n")
	preview.WriteString(tailBytes(full, toolResultTailBytes))
	preview.WriteString("\n\n")
	if spilled {
		fmt.Fprintf(&preview,
			"[output too large: %d bytes total; full output saved to %s — read it with offset/limit, or download it after the task halts]",
			total, ref)
	} else {
		fmt.Fprintf(&preview,
			"[output too large: %d bytes total; truncated to fit the context budget]", total)
	}
	// Final clamp guarantees the inline output never exceeds the budget even if
	// the marker is longer than the reserve.
	out := headBytes(preview.String(), toolResultBudgetBytes)
	result.Output = []*aop.Content{aop.Text(out)}
}

// spillToolResultOutput writes the full output under the workspace and returns
// the workspace-relative path the read tool can open. It reports false when there
// is no usable workspace or the write fails, in which case the result is only
// truncated.
func spillToolResultOutput(workDir, callID, full string) (string, bool) {
	if workDir == "" {
		return "", false
	}
	dir := filepath.Join(workDir, spillDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false
	}
	name := sanitizeSpillFilename(callID) + ".log"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(full), 0o600); err != nil {
		return "", false
	}
	return filepath.Join(spillDirName, name), true
}

func sanitizeSpillFilename(callID string) string {
	if callID == "" {
		return "output"
	}
	var b strings.Builder
	for _, r := range callID {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// headBytes returns the first n bytes of s, cut on a rune boundary. The input is
// already valid UTF-8 (results are sanitized before bounding), so this only avoids
// splitting a multi-byte rune at the cut point.
func headBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	end := n
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

// tailBytes returns the last n bytes of s, cut on a rune boundary.
func tailBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}
