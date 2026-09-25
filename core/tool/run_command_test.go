package tool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRunCommandRegisteredPrecedence(t *testing.T) {
	registry, _ := loadTestRegistry(t, commandBatch(Command{
		Name: "echo", Run: func(_ context.Context, execution *Execution) (any, error) {
			fmt.Fprint(execution.Stdout, "registered:", strings.Join(execution.Args, ","))
			return "details", nil
		},
	}))
	var output bytes.Buffer
	details, err := RunCommand(t.Context(), registry, []string{"echo", "one", "two"}, &Execution{Stdout: &output})
	if err != nil || details != "details" || output.String() != "registered:one,two" {
		t.Fatalf("details=%v output=%q err=%v", details, output.String(), err)
	}
}

func TestRunCommandExternalEnvironmentStreamsAndStatus(t *testing.T) {
	var output bytes.Buffer
	parent := &Execution{
		Dir: t.TempDir(), Env: []string{"RUN_COMMAND_CHILD=1", "RUN_COMMAND_VALUE=override"},
		Stdin: strings.NewReader("input"), Stdout: &output, Stderr: &output,
	}
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	_, err = RunCommand(t.Context(), nil, []string{program, "-test.run=^TestRunCommandChild$"}, parent)
	if err != nil || !strings.Contains(output.String(), "override:input:") || !strings.Contains(output.String(), parent.Dir) {
		t.Fatalf("output=%q err=%v", output.String(), err)
	}
	parent.Env = append(parent.Env, "RUN_COMMAND_FAIL=7")
	_, err = RunCommand(t.Context(), nil, []string{program, "-test.run=^TestRunCommandChild$"}, parent)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("exit=%v, want status 7", err)
	}
}

func TestRunCommandCancellation(t *testing.T) {
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err = RunCommand(ctx, nil, []string{program, "-test.run=^TestRunCommandChild$"}, &Execution{
		Env:    []string{"RUN_COMMAND_CHILD=1", "RUN_COMMAND_SLEEP=1"},
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestRunCommandChild(t *testing.T) {
	if os.Getenv("RUN_COMMAND_CHILD") != "1" {
		return
	}
	if os.Getenv("RUN_COMMAND_SLEEP") == "1" {
		time.Sleep(10 * time.Second)
		return
	}
	input, _ := io.ReadAll(os.Stdin)
	dir, _ := os.Getwd()
	fmt.Printf("%s:%s:%s", os.Getenv("RUN_COMMAND_VALUE"), input, dir)
	if os.Getenv("RUN_COMMAND_FAIL") == "7" {
		os.Exit(7)
	}
}
