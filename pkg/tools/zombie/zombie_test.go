package zombie

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

func TestExecuteDebugActivatesTelemetryLogger(t *testing.T) {
	var logs bytes.Buffer
	cmd := New(nil).WithLogger(telemetry.NewLogger(telemetry.LogConfig{Output: &logs}))

	commands.Output.Reset(nil)
	if err := cmd.Execute(context.Background(), []string{"--debug", "--help"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := logs.String(); !strings.Contains(got, "[debug] zombie debug enabled") {
		t.Fatalf("debug logs = %q", got)
	}
}

func TestResolveRelativePathsOnlyRewritesZombieFileFlags(t *testing.T) {
	dir := t.TempDir()
	cmd := New(nil)
	cmd.SetWorkDir(dir)

	got := cmd.resolveRelativePaths([]string{
		"-l",
		"-o", "string",
		"-I", "ips.txt",
		"--IP=more-ips.txt",
		"-U", "users.txt",
		"--USER=more-users.txt",
		"-P", "pwds.txt",
		"--PWD=more-pwds.txt",
		"-A", "auth.txt",
		"--AUTH=more-auth.txt",
		"-j", "scan.json",
		"--json=more-scan.json",
		"-g", "gogo.json",
		"--gogo=more-gogo.json",
		"-f", "out.json",
		"--file=more-out.json",
	})
	want := []string{
		"-l",
		"-o", "string",
		"-I", filepath.Join(dir, "ips.txt"),
		"--IP=" + filepath.Join(dir, "more-ips.txt"),
		"-U", filepath.Join(dir, "users.txt"),
		"--USER=" + filepath.Join(dir, "more-users.txt"),
		"-P", filepath.Join(dir, "pwds.txt"),
		"--PWD=" + filepath.Join(dir, "more-pwds.txt"),
		"-A", filepath.Join(dir, "auth.txt"),
		"--AUTH=" + filepath.Join(dir, "more-auth.txt"),
		"-j", filepath.Join(dir, "scan.json"),
		"--json=" + filepath.Join(dir, "more-scan.json"),
		"-g", filepath.Join(dir, "gogo.json"),
		"--gogo=" + filepath.Join(dir, "more-gogo.json"),
		"-f", filepath.Join(dir, "out.json"),
		"--file=" + filepath.Join(dir, "more-out.json"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveRelativePaths() = %#v, want %#v", got, want)
	}
}
