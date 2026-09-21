package scan

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestExecutionOnlyRejectsAIModesBeforeExecuting(t *testing.T) {
	command := New(nil, WithExecutionOnly())
	for _, args := range [][]string{{"--sniper"}, {"--deep"}, {"--verify", "high"}, {"--verify", "auto"}} {
		_, _, err := command.execute(context.Background(), args, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "AI modes are unavailable") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}
