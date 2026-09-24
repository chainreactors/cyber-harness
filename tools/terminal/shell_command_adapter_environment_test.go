package terminal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShellCommandAdapterEnvironmentHelper(t *testing.T) {
	if os.Getenv("CYBER_TEST_ADAPTER_ENVIRONMENT") != "1" {
		return
	}
	fmt.Printf("proxy TERM=%s command=%s\n", os.Getenv("TERM"), os.Getenv(shellCommandAdapterCommandEnv))
	os.Exit(7)
}

func TestShellCommandAdapterScopesTerminalEnvironment(t *testing.T) {
	// Cover both Bash function names and names that need executable shims on
	// Windows. Reexecute this test binary to observe the real child environment.
	for _, name := range []string{"terminal_probe", "terminal-probe"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TERM", "xterm-256color")
			t.Setenv("CYBER_TEST_ADAPTER_ENVIRONMENT", "1")
			t.Setenv(shellCommandAdapterMarkerEnv, "")
			t.Setenv("GORACE", strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if _, err := createShellCommandAdapterAlias(executable, dir, name); err != nil {
				t.Fatal(err)
			}
			if err := writeShellCommandBashEnv(dir, []string{name}); err != nil {
				t.Fatal(err)
			}
			t.Setenv(shellCommandAdapterExecutableEnv, executable)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("BASH_ENV", shellBashEnv(dir))
			const helper = "-test.run=^TestShellCommandAdapterEnvironmentHelper$"
			script := name + " " + helper + "; adapter_status=$?; printf 'caller TERM=%s\\n' \"$TERM\"; exit \"$adapter_status\""
			if !posixShellAvailable() {
				parent := filepath.Join(dir, "parent.cmd")
				batch := "@echo off\r\ncall " + name + " " + helper + "\r\nset adapter_status=%errorlevel%\r\necho caller TERM=%TERM%\r\nexit /b %adapter_status%\r\n"
				if err := os.WriteFile(parent, []byte(batch), 0o600); err != nil {
					t.Fatal(err)
				}
				script = `"` + parent + `"`
			}
			binary, args := pipeShell(script)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Env = prepareShellProcessEnv(os.Environ())
			output, err := cmd.CombinedOutput()
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 7 {
				t.Fatalf("adapter did not preserve exit status 7: %v\n%s", err, output)
			}
			for _, want := range []string{"proxy TERM=dumb command=" + name, "caller TERM=xterm-256color"} {
				if !strings.Contains(string(output), want) {
					t.Fatalf("missing %q in child/caller output: %s", want, output)
				}
			}
		})
	}
}
