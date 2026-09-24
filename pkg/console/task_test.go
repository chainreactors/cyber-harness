package console

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/aop"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func TestRunTaskFinalizesOnceBeforeResult(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "completed turn"
		if canceled {
			name = "session startup canceled"
		}
		t.Run(name, func(t *testing.T) {
			rt, _ := newConsoleRuntime(t, &consoleProvider{})
			path := filepath.Join(t.TempDir(), "stdout.json")
			output, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			original := os.Stdout
			os.Stdout = output
			defer func() { os.Stdout = original; output.Close() }()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if canceled {
				cancel()
			}
			failure := errors.New("report finalization failed")
			calls := 0
			finalize := func(runErr error) error {
				calls++
				if errors.Is(runErr, context.Canceled) != canceled {
					t.Errorf("unexpected task error: %v", runErr)
				}
				if info, err := output.Stat(); err != nil || info.Size() != 0 {
					t.Error("JSON was emitted before finalization")
				}
				return errors.Join(runErr, failure)
			}
			option := cfg.Option{MiscOptions: cfg.MiscOptions{OutputFormat: "json"}}
			err = RunTask(ctx, rt, &option, "task", "test", "review", agentsession.RunInput{Content: []*aop.Content{aop.Text("review")}}, finalize)
			if calls != 1 || !errors.Is(err, failure) {
				t.Fatalf("finalizer calls=%d error=%v", calls, err)
			}
			output.Close()
			os.Stdout = original
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var result machineResult
			if err := json.Unmarshal(body, &result); err != nil {
				t.Fatal(err)
			}
			wantStop := "error"
			if canceled {
				wantStop = "canceled"
			}
			if !result.IsError || result.Error == nil || result.StopReason != wantStop {
				t.Fatalf("incorrect final result: %+v", result)
			}
		})
	}
}
