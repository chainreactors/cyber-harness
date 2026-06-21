package gogo

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	gogopkg "github.com/chainreactors/gogo/v2/pkg"
	sdkgogo "github.com/chainreactors/sdk/gogo"
)

func TestExecuteInstallsResourceProviderBeforePrepare(t *testing.T) {
	defer gogopkg.ResetResourceProvider()

	var calls atomic.Int32
	engine, err := sdkgogo.NewEngine(sdkgogo.NewConfig().WithResourceProvider(func(string) []byte {
		calls.Add(1)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	commands.Output.Reset(nil)
	err = New(engine).Execute(context.Background(), []string{"-P", "extract"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if calls.Load() == 0 {
		t.Fatal("resource provider was not called during gogo prepare")
	}
}

func TestExecuteDebugActivatesTelemetryLogger(t *testing.T) {
	var logs bytes.Buffer
	cmd := New(nil).WithLogger(telemetry.NewLogger(telemetry.LogConfig{Output: &logs}))

	commands.Output.Reset(nil)
	if err := cmd.Execute(context.Background(), []string{"--debug", "--help"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := logs.String(); !strings.Contains(got, "[debug] gogo debug enabled") {
		t.Fatalf("debug logs = %q", got)
	}
}

func TestNormalizeArgsKeepsOutputFormatsAndResolvesFiles(t *testing.T) {
	dir := t.TempDir()
	cmd := New(nil)
	cmd.SetWorkDir(dir)

	got := cmd.normalizeArgs([]string{
		"-i", "127.0.0.1",
		"-o", "jsonl",
		"-f", "out.jsonl",
		"--json", "previous.dat",
		"--file-output=jsonl",
		"--exclude-file=exclude.txt",
	})
	want := []string{
		"-i", "127.0.0.1",
		"-o", "jl",
		"-f", filepath.Join(dir, "out.jsonl"),
		"--json", filepath.Join(dir, "previous.dat"),
		"--file-output=jl",
		"--exclude-file=" + filepath.Join(dir, "exclude.txt"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeArgs() = %#v, want %#v", got, want)
	}
}

func TestNormalizeArgsConvertsValuelessJSONFlag(t *testing.T) {
	cmd := New(nil)
	got := cmd.normalizeArgs([]string{"-i", "127.0.0.1", "-j", "-t", "100"})
	want := []string{"-i", "127.0.0.1", "-o", "jl", "-t", "100"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeArgs() = %#v, want %#v", got, want)
	}
}

