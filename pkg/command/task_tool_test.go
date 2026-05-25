package command

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/task"
)

func TestTaskToolPeekNewLimitsAndPreservesOverflow(t *testing.T) {
	mgr := task.NewManager()
	payload := strings.Repeat("x", peekNewMaxBytes+7)
	fn := func(ctx context.Context, out io.Writer) error {
		_, _ = io.WriteString(out, payload)
		return nil
	}
	info, err := mgr.SpawnInProcess("large-output", "large-output", 10*time.Second, fn)
	if err != nil {
		t.Fatalf("SpawnInProcess: %v", err)
	}
	final, err := mgr.Wait(context.Background(), info.ID, 3*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if final.State != task.StateCompleted {
		t.Fatalf("state = %s, want completed", final.State)
	}

	tool := NewTaskTool(mgr)
	args := fmt.Sprintf(`{"action":"peek_new","id":%q}`, info.ID)
	res1, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("first peek_new: %v", err)
	}
	out1 := res1.Text()
	firstChunk := strings.Repeat("x", peekNewMaxBytes)
	if !strings.HasPrefix(out1, firstChunk+"\n\n[more output available") {
		t.Fatalf("first peek_new did not return exactly one capped payload chunk followed by marker")
	}
	if !strings.Contains(out1, "more output available") {
		t.Fatalf("first peek_new missing overflow marker")
	}

	res2, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("second peek_new: %v", err)
	}
	if res2.Text() != strings.Repeat("x", 7) {
		t.Fatalf("second peek_new = %q, want remaining payload", res2.Text())
	}
}
