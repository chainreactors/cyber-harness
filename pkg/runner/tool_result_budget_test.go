package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/tool"
)

func textResult(text string) *aop.ToolResult {
	return &aop.ToolResult{Output: []*aop.Content{aop.Text(text)}}
}

func resultText(result *aop.ToolResult) string {
	var sb strings.Builder
	for _, content := range result.Output {
		if text := content.GetText(); text != nil {
			sb.WriteString(text.Text)
		}
	}
	return sb.String()
}

func TestBoundToolResultOutput_UnderBudgetUnchanged(t *testing.T) {
	ctx := tool.ContextWithInvocation(context.Background(), tool.Invocation{WorkDir: t.TempDir(), CallID: "small"})
	small := strings.Repeat("ok\n", 100)
	result := textResult(small)

	boundToolResultOutput(ctx, result)

	if got := resultText(result); got != small {
		t.Fatalf("under-budget result changed: got %d bytes, want %d", len(got), len(small))
	}
}

func TestBoundToolResultOutput_OverBudgetSpillsAndBounds(t *testing.T) {
	workDir := t.TempDir()
	ctx := tool.ContextWithInvocation(context.Background(), tool.Invocation{WorkDir: workDir, CallID: "big-call"})

	// 5 MiB of distinctly-marked content so we can verify head and tail survive.
	head := "HEAD-OF-OUTPUT "
	tail := " TAIL-OF-OUTPUT"
	full := head + strings.Repeat("x", 5<<20) + tail
	result := textResult(full)
	result.CallId = "big-call" // ExecuteToolRequest sets CallId before bounding

	boundToolResultOutput(ctx, result)

	got := resultText(result)
	if len(got) > toolResultBudgetBytes {
		t.Fatalf("bounded result = %d bytes, want <= %d", len(got), toolResultBudgetBytes)
	}
	if !strings.Contains(got, "HEAD-OF-OUTPUT") {
		t.Errorf("preview lost the head of the output")
	}
	if !strings.Contains(got, "TAIL-OF-OUTPUT") {
		t.Errorf("preview lost the tail of the output")
	}
	if !strings.Contains(got, "full output saved to") {
		t.Errorf("preview missing spill reference, got tail: %q", got[len(got)-160:])
	}

	// The spilled file must exist under the workspace and hold the full output.
	matches, err := filepath.Glob(filepath.Join(workDir, spillDirName, "*.log"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected exactly one spill file, got %v, err=%v", matches, err)
	}
	spilled, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read spill file: %v", err)
	}
	if string(spilled) != full {
		t.Errorf("spilled content = %d bytes, want full %d bytes", len(spilled), len(full))
	}

	// The reference the model sees must be workspace-relative so `read` resolves it.
	rel := filepath.Join(spillDirName, "big-call.log")
	if !strings.Contains(got, rel) {
		t.Errorf("preview reference %q not workspace-relative %q", got, rel)
	}
	if _, err := os.Stat(filepath.Join(workDir, rel)); err != nil {
		t.Errorf("referenced path not readable from workspace: %v", err)
	}
}

func TestBoundToolResultOutput_NoWorkDirHardTruncates(t *testing.T) {
	ctx := tool.ContextWithInvocation(context.Background(), tool.Invocation{WorkDir: "", CallID: "nowd"})
	full := strings.Repeat("y", 5<<20)
	result := textResult(full)

	boundToolResultOutput(ctx, result)

	got := resultText(result)
	if len(got) > toolResultBudgetBytes {
		t.Fatalf("bounded result = %d bytes, want <= %d", len(got), toolResultBudgetBytes)
	}
	if !strings.Contains(got, "truncated to fit the context budget") {
		t.Errorf("expected hard-truncate marker when no workspace, got tail: %q", got[len(got)-160:])
	}
}
