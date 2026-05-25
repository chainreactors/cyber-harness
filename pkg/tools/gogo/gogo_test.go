package gogo

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	gogopkg "github.com/chainreactors/gogo/v2/pkg"
	sdkgogo "github.com/chainreactors/sdk/gogo"
)

func TestExecuteInstallsResourceProviderBeforePrepare(t *testing.T) {
	defer gogopkg.ResetResourceProvider()

	var calls atomic.Int32
	engine := sdkgogo.NewEngine(sdkgogo.NewConfig().WithResourceProvider(func(string) []byte {
		calls.Add(1)
		return nil
	}))

	_, err := New(engine).Execute(context.Background(), []string{"-P", "extract"})
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

	if _, err := cmd.Execute(context.Background(), []string{"--debug", "--help"}); err != nil {
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

func TestParseSDKScanArgsSupportsListFileModAndPing(t *testing.T) {
	listPath := filepath.Join(t.TempDir(), "targets.txt")
	if err := os.WriteFile(listPath, []byte("# comment\n127.0.0.1\n10.0.0.0/30\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts, ok, err := parseSDKScanArgs([]string{"-L", listPath, "-p", "80", "--ping", "-m", "s"})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected args to map to SDK scan path")
	}
	if opts.target != "127.0.0.1,10.0.0.0/30" {
		t.Fatalf("target = %q", opts.target)
	}
	if opts.ports != "80" {
		t.Fatalf("ports = %q", opts.ports)
	}
	if opts.mod != "s" {
		t.Fatalf("mod = %q", opts.mod)
	}
	if !opts.ping {
		t.Fatal("ping should be enabled")
	}
}

func TestParseSDKScanArgsReportsListFileErrors(t *testing.T) {
	opts, ok, err := parseSDKScanArgs([]string{"--list", filepath.Join(t.TempDir(), "missing.txt")})
	if err == nil {
		t.Fatal("expected list file error")
	}
	if ok || opts != nil {
		t.Fatalf("opts = %#v, ok = %v; want nil, false", opts, ok)
	}
}

func TestParseSDKScanArgsRejectsUnsupportedEqualsFlags(t *testing.T) {
	opts, ok, err := parseSDKScanArgs([]string{"-i", "127.0.0.1", "--workflow=wf.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if ok || opts != nil {
		t.Fatalf("opts = %#v, ok = %v; want nil, false", opts, ok)
	}
}

func TestParseSDKScanArgsSupportsFalsePingValue(t *testing.T) {
	opts, ok, err := parseSDKScanArgs([]string{"-i", "127.0.0.1", "--ping=false"})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected SDK scan path")
	}
	if opts.ping {
		t.Fatal("ping should be false")
	}
}
