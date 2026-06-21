package spray

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
	sdkspray "github.com/chainreactors/sdk/spray"
	spraypkg "github.com/chainreactors/spray/pkg"
)

func TestWithDefaultNoBarAppendsFlag(t *testing.T) {
	got := withDefaultNoBar([]string{"-u", "http://127.0.0.1", "--finger"})
	want := []string{"-u", "http://127.0.0.1", "--finger", "--no-bar"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("withDefaultNoBar() = %#v, want %#v", got, want)
	}
}

func TestWithDefaultNoBarKeepsExplicitFlag(t *testing.T) {
	args := []string{"-u", "http://127.0.0.1", "--no-bar=false"}
	got := withDefaultNoBar(args)
	if !reflect.DeepEqual(got, args) {
		t.Fatalf("withDefaultNoBar() = %#v, want %#v", got, args)
	}
}

func TestWithDefaultNoStatAppendsFlag(t *testing.T) {
	got := withDefaultNoStat([]string{"-u", "http://127.0.0.1", "--finger"})
	want := []string{"-u", "http://127.0.0.1", "--finger", "--no-stat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("withDefaultNoStat() = %#v, want %#v", got, want)
	}
}

func TestWithDefaultNoStatKeepsExplicitFlag(t *testing.T) {
	args := []string{"-u", "http://127.0.0.1", "--no-stat=false"}
	got := withDefaultNoStat(args)
	if !reflect.DeepEqual(got, args) {
		t.Fatalf("withDefaultNoStat() = %#v, want %#v", got, args)
	}
}

func TestWithDefaultScannerFlagsAppendsFlags(t *testing.T) {
	got := withDefaultScannerFlags([]string{"-u", "http://127.0.0.1"})
	want := []string{"-u", "http://127.0.0.1", "--no-bar", "--no-stat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("withDefaultScannerFlags() = %#v, want %#v", got, want)
	}
}

func TestResolveRelativePathsOnlyRewritesSprayFileFlags(t *testing.T) {
	dir := t.TempDir()
	cmd := New(nil)
	cmd.SetWorkDir(dir)

	got := cmd.resolveRelativePaths([]string{
		"-l", "targets.txt",
		"-w", "admin{?ld#2}",
		"-o", "full",
		"-d", "dict.txt",
		"--dict=more.txt",
		"-r", "rules.txt",
		"--rules=more-rules.txt",
		"-R", "append.rule",
		"--append=append.txt",
		"--raw", "request.txt",
		"-f", "out.json",
		"--dump-file=dump.jsonl",
		"-c", "spray.yaml",
		"--config=custom.yaml",
		"--extract-config", "extract.yaml",
	})
	want := []string{
		"-l", filepath.Join(dir, "targets.txt"),
		"-w", "admin{?ld#2}",
		"-o", "full",
		"-d", filepath.Join(dir, "dict.txt"),
		"--dict=" + filepath.Join(dir, "more.txt"),
		"-r", filepath.Join(dir, "rules.txt"),
		"--rules=" + filepath.Join(dir, "more-rules.txt"),
		"-R", filepath.Join(dir, "append.rule"),
		"--append=" + filepath.Join(dir, "append.txt"),
		"--raw", filepath.Join(dir, "request.txt"),
		"-f", filepath.Join(dir, "out.json"),
		"--dump-file=" + filepath.Join(dir, "dump.jsonl"),
		"-c", filepath.Join(dir, "spray.yaml"),
		"--config=" + filepath.Join(dir, "custom.yaml"),
		"--extract-config", filepath.Join(dir, "extract.yaml"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveRelativePaths() = %#v, want %#v", got, want)
	}
}

func TestExecuteInstallsResourceProviderBeforePrint(t *testing.T) {
	defer spraypkg.ResetResourceProvider()

	var calls atomic.Int32
	engine, err := sdkspray.NewEngine(sdkspray.NewConfig().WithResourceProvider(func(name string) []byte {
		calls.Add(1)
		switch name {
		case "http", "socket":
			return []byte("[]")
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	commands.Output.Reset(nil)
	err = New(engine).Execute(context.Background(), []string{"--print"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if calls.Load() == 0 {
		t.Fatal("resource provider was not called during spray print")
	}
}

func TestExecuteDebugActivatesTelemetryLogger(t *testing.T) {
	var logs bytes.Buffer
	cmd := New(nil).WithLogger(telemetry.NewLogger(telemetry.LogConfig{Output: &logs}))

	commands.Output.Reset(nil)
	if err := cmd.Execute(context.Background(), []string{"--debug", "--help"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := logs.String(); !strings.Contains(got, "[debug] spray debug enabled") {
		t.Fatalf("debug logs = %q", got)
	}
}
