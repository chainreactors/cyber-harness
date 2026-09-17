package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type routingContainment struct {
	commands []string
	cleaned  chan struct{}
}

func (c *routingContainment) Prepare(command string, options BashExecOptions) (BashExecOptions, func(), error) {
	c.commands = append(c.commands, command)
	return options, func() {
		if c.cleaned != nil {
			close(c.cleaned)
		}
	}, nil
}

func TestBashShellSyntaxRouting(t *testing.T) {
	tests := []struct {
		name, command, want string
	}{
		{"semicolon", "memory_echo one;memory_echo two", "one\ntwo"},
		{"and", "memory_echo one&&memory_echo two", "one\ntwo"},
		{"or", "memory_fail ignored||memory_echo recovered", "adapter test exit 7\nrecovered"},
		{"short circuit", "memory_echo one||memory_echo unreachable", "one"},
		{"newline", "memory_echo one\nmemory_echo two", "one\ntwo"},
		{"pipeline", "memory_echo one|memory_upper", "ONE"},
		{"background", "memory_echo one& wait", "one"},
		{"negation", "! memory_fail ignored", "adapter test exit 7"},
		{"environment", `memory_echo "$ROUTING_VALUE"`, "expanded"},
		{"substitution", `memory_echo "$(printf substituted)"`, "substituted"},
		{"braces", "memory_echo {one,two}", "one two"},
		{"glob", "memory_echo *.txt", "one.txt two.txt"},
		{"redirection", "memory_echo one>result;cat result", "one"},
		{"descriptor duplication", "memory_echo one 2>&1", "one"},
		{"literal through shell", "memory_echo ';' '|' '>' ''&&memory_echo done", "; | > \ndone"},
		{"heredoc", "memory_upper <<'EOF'\n# keep this\n\nlast\nEOF\n", "# KEEP THIS\n\nLAST"},
		{"multiline quote", "memory_echo 'first\n# keep this\n\nlast'", "first\n# keep this\n\nlast"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bash, _, _ := newAdapterTestBash(t)
			bash.WithEnvironment(map[string]string{"ROUTING_VALUE": "expanded"})
			containment := &routingContainment{}
			bash.WithProcessContainment(containment)
			dir := t.TempDir()
			for _, name := range []string{"one.txt", "two.txt"} {
				if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			info, output := runAdapterCommand(t, bash, t.Context(), tt.command, dir)
			output = strings.TrimSpace(strings.ReplaceAll(output, "\r\n", "\n"))
			if info.ExitCode != 0 || output != tt.want {
				t.Fatalf("exit=%d output=%q, want %q", info.ExitCode, output, tt.want)
			}
			wantShell := tt.name != "multiline quote"
			if wantShell && !reflect.DeepEqual(containment.commands, []string{tt.command}) {
				t.Fatalf("supervised scripts = %q, want original script %q", containment.commands, tt.command)
			}
			if !wantShell && (len(containment.commands) != 0 || bash.shellAdapter != nil) {
				t.Fatal("literal command unnecessarily started a shell")
			}
		})
	}
}

func TestBashLiteralArguments(t *testing.T) {
	tests := []struct {
		command string
		want    []string
	}{
		{`capture 'file;name.json' ";" '|' '&&' '>' '2>&1'`, []string{"file;name.json", ";", "|", "&&", ">", "2>&1"}},
		{`capture file\;name.json a\ b \* \~`, []string{"file;name.json", "a b", "*", "~"}},
		{`capture '' "" pre'fix'"suffix"`, []string{"", "", "prefixsuffix"}},
		{`capture '$HOME' "\$HOME" '\n' "\n"`, []string{"$HOME", "$HOME", `\n`, `\n`}},
		{"# comment\ncapture value # ignored\n", []string{"value"}},
		{"capture va\\\nlue", []string{"value"}},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			var got []string
			registry, _ := loadTestRegistry(t, commandGroup("capture", "test", Command{
				Name: "capture", Run: func(_ context.Context, e *Execution) (any, error) {
					got = append([]string(nil), e.Args...)
					return nil, nil
				},
			}))
			bash := NewBashTool(t.TempDir(), 5, nil)
			bash.EnableShellCommands(registry)
			t.Cleanup(bash.Close)
			containment := &routingContainment{}
			bash.WithProcessContainment(containment)
			info, _ := runAdapterCommand(t, bash, t.Context(), tt.command, "")
			if info.ExitCode != 0 || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("exit=%d args=%q, want %q", info.ExitCode, got, tt.want)
			}
			if bash.shellAdapter != nil || len(containment.commands) != 0 {
				t.Fatal("literal arguments should use the direct command path")
			}
		})
	}
}

func TestBashCompoundCommandWithoutAdapter(t *testing.T) {
	for _, command := range []string{
		"sample value;echo extra", "sample value&&echo extra", "sample value||echo extra",
		"sample value\necho extra", "sample value&", "sample $VALUE", "sample value|&cat",
		"sample value|cat;echo extra", "sample value|cat&&echo extra",
		"sample one|sample two", "sample one|cat|sample two", "! sample value|cat",
		"cat <<'EOF'|sample\nbody\nEOF\n", "sample 'unterminated",
	} {
		t.Run(command, func(t *testing.T) {
			bash := newBashWithPseudo(t, t.TempDir(), &outputCommand{name: "sample", output: "wrong"})
			t.Cleanup(bash.Close)
			if _, err := bash.RunForeground(t.Context(), command, BashExecOptions{}); err == nil {
				t.Fatal("unsupported composition must not execute as a direct command")
			}
		})
	}
}

func TestBashBuiltinDownloadThenCount(t *testing.T) {
	const payload = `{"ok":true}`
	registry, _ := loadTestRegistry(t, commandGroup("curl", "test", Command{
		Name: "curl", Run: func(_ context.Context, e *Execution) (any, error) {
			want := []string{"https://example.invalid/data", "-o", "file.json"}
			if !reflect.DeepEqual(e.Args, want) {
				return nil, fmt.Errorf("curl args=%q, want %q", e.Args, want)
			}
			return nil, os.WriteFile(filepath.Join(e.Dir, e.Args[2]), []byte(payload), 0600)
		},
	}))
	bash := NewBashTool(t.TempDir(), 5, nil)
	bash.EnableShellCommands(registry)
	t.Cleanup(bash.Close)
	dir := t.TempDir()
	info, output := runAdapterCommand(t, bash, t.Context(), "curl https://example.invalid/data -o file.json; wc -c < file.json", dir)
	if info.ExitCode != 0 || strings.TrimSpace(output) != fmt.Sprint(len(payload)) {
		t.Fatalf("exit=%d output=%q, want byte count %d", info.ExitCode, output, len(payload))
	}
	data, err := os.ReadFile(filepath.Join(dir, "file.json"))
	if err != nil || string(data) != payload {
		t.Fatalf("downloaded file=%q err=%v", data, err)
	}
}

func TestBashGluedCommandCancellation(t *testing.T) {
	bash, _, state := newAdapterTestBash(t)
	containment := &routingContainment{cleaned: make(chan struct{})}
	bash.WithProcessContainment(containment)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	execution, err := bash.Start(ctx, "memory_wait ignored;memory_echo unreachable", BashExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-state.started:
	case <-time.After(5 * time.Second):
		t.Fatal("registered command did not start")
	}
	cancel()
	for name, done := range map[string]<-chan struct{}{
		"registered command cancellation": state.canceled,
		"shell containment cleanup":       containment.cleaned,
		"session completion":              bash.tasks.Done(execution.ID),
	} {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %s", name)
		}
	}
	if len(containment.commands) != 1 {
		t.Fatalf("supervised scripts=%q", containment.commands)
	}
}

func TestRegistryRunPreservesArgv(t *testing.T) {
	args := []string{";", "|", ">file", "2>&1", ""}
	registry, _ := loadTestRegistry(t, commandGroup("capture", "test", Command{
		Name: "capture", Run: func(_ context.Context, e *Execution) (any, error) {
			if !reflect.DeepEqual(e.Args, args) {
				return nil, fmt.Errorf("args=%q, want %q", e.Args, args)
			}
			return nil, nil
		},
	}))
	if _, err := registry.Run(t.Context(), append([]string{"capture"}, args...), &Execution{}); err != nil {
		t.Fatal(err)
	}
}
