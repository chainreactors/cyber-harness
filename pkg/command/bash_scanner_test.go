package command_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/chainreactors/aiscan/pkg/command"
)

func TestScannerSubcommandsThroughBash(t *testing.T) {
	registry := command.NewRegistry()
	commands := map[string]*recordingCommand{
		"gogo":    newRecordingCommand("gogo"),
		"spray":   newRecordingCommand("spray"),
		"zombie":  newRecordingCommand("zombie"),
		"neutron": newRecordingCommand("neutron"),
	}
	for _, name := range []string{"gogo", "spray", "zombie", "neutron"} {
		registry.Register(commands[name], "")
	}

	bash := command.NewBashTool(t.TempDir(), 5, registry)
	tests := []struct {
		name string
		cmd  string
		args []string
	}{
		{
			name: "gogo",
			cmd:  "gogo -i 127.0.0.1 -p 80,443 -t 10 -d 1 -vv",
			args: []string{"-i", "127.0.0.1", "-p", "80,443", "-t", "10", "-d", "1", "-vv"},
		},
		{
			name: "spray",
			cmd:  `spray -u "http://127.0.0.1/a b" -T 1 -t 5 --finger`,
			args: []string{"-u", "http://127.0.0.1/a b", "-T", "1", "-t", "5", "--finger"},
		},
		{
			name: "zombie",
			cmd:  "zombie -i ssh://root@127.0.0.1:22 -p pass -t 1 --top 3",
			args: []string{"-i", "ssh://root@127.0.0.1:22", "-p", "pass", "-t", "1", "--top", "3"},
		},
		{
			name: "neutron",
			cmd:  "neutron -i http://127.0.0.1 --finger nginx",
			args: []string{"-i", "http://127.0.0.1", "--finger", "nginx"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := bash.Execute(context.Background(), bashArgs(tt.cmd))
			if err != nil {
				t.Fatalf("bash.Execute() error = %v", err)
			}
			if !strings.Contains(out, "["+tt.name+"] ok") {
				t.Fatalf("output = %q, want command output", out)
			}
			if got := commands[tt.name].lastArgs(); !reflect.DeepEqual(got, tt.args) {
				t.Fatalf("args = %#v, want %#v", got, tt.args)
			}
		})
	}
}

type recordingCommand struct {
	name   string
	output string

	mu   sync.Mutex
	args [][]string
}

func newRecordingCommand(name string) *recordingCommand {
	return &recordingCommand{name: name}
}

func (c *recordingCommand) Name() string { return c.name }

func (c *recordingCommand) Usage() string {
	return fmt.Sprintf("%s - test command\nUsage: %s [options]", c.name, c.name)
}

func (c *recordingCommand) Execute(_ context.Context, args []string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	copied := append([]string(nil), args...)
	c.args = append(c.args, copied)
	if c.output != "" {
		return c.output, nil
	}
	return fmt.Sprintf("[%s] ok args=%s", c.name, strings.Join(args, " ")), nil
}

func (c *recordingCommand) lastArgs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.args) == 0 {
		return nil
	}
	return append([]string(nil), c.args[len(c.args)-1]...)
}

func TestScannerRejectsShellPipeAndFileRedir(t *testing.T) {
	registry := command.NewRegistry()
	registry.Register(newRecordingCommand("spray"), "")
	bash := command.NewBashTool(t.TempDir(), 5, registry)

	tests := []struct {
		name     string
		cmd      string
		wantHint string
	}{
		{
			name:     "pipe to head",
			cmd:      `spray -u http://x | head -30`,
			wantHint: "shell pipes",
		},
		{
			name:     "pipe to grep through 2>&1",
			cmd:      `spray -u http://x 2>&1 | grep valid`,
			wantHint: "shell pipes",
		},
		{
			name:     "double pipe",
			cmd:      `spray -u http://x || echo done`,
			wantHint: "shell pipes",
		},
		{
			name:     "file redirection >",
			cmd:      `spray -u http://x > out.txt`,
			wantHint: "file redirection",
		},
		{
			name:     "file redirection >>",
			cmd:      `spray -u http://x >> out.txt`,
			wantHint: "file redirection",
		},
		{
			name:     "stderr to file",
			cmd:      `spray -u http://x 2>err.log`,
			wantHint: "file redirection",
		},
		{
			name:     "combined to file",
			cmd:      `spray -u http://x &> all.log`,
			wantHint: "file redirection",
		},
		{
			name:     "chained with &&",
			cmd:      `spray -u http://x && spray -u http://y`,
			wantHint: "chaining",
		},
		{
			name:     "chained with ;",
			cmd:      `spray -u http://x ; echo done`,
			wantHint: "chaining",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := bash.Execute(context.Background(), bashArgs(tt.cmd))
			if err == nil {
				t.Fatalf("expected error, got output %q", out)
			}
			if !strings.Contains(err.Error(), tt.wantHint) {
				t.Fatalf("error = %v, want hint containing %q", err, tt.wantHint)
			}
		})
	}
}

func TestScannerStripsInertStderrDup(t *testing.T) {
	// 2>&1 has no semantic effect for an in-process command because the
	// pseudo-command already returns combined output as the tool result.
	// It should be silently stripped without rejecting the call.
	registry := command.NewRegistry()
	rec := newRecordingCommand("spray")
	registry.Register(rec, "")
	bash := command.NewBashTool(t.TempDir(), 5, registry)

	out, err := bash.Execute(context.Background(), bashArgs(`spray -u http://x 2>&1`))
	if err != nil {
		t.Fatalf("bash.Execute() error = %v", err)
	}
	if !strings.Contains(out, "[spray] ok") {
		t.Fatalf("output = %q, want spray output", out)
	}
	want := []string{"-u", "http://x"}
	if got := rec.lastArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v (2>&1 should be stripped)", got, want)
	}
}

func TestScannerPseudoCommandForegroundUsesCallerContext(t *testing.T) {
	// Foreground pseudo-commands should preserve the caller's context instead
	// of inheriting the bash shell timeout. Long whole-task scans should use
	// background:true.
	registry := command.NewRegistry()
	registry.Register(&deadlineRecordingCommand{name: "spray"}, "")
	bash := command.NewBashTool(t.TempDir(), 1, registry)

	out, err := bash.Execute(context.Background(), bashArgs("spray -u http://x"))
	if err != nil {
		t.Fatalf("bash.Execute() error = %v", err)
	}
	if out != "no deadline" {
		t.Fatalf("output = %q, want no deadline", out)
	}
}

type deadlineRecordingCommand struct {
	name string
}

func (c *deadlineRecordingCommand) Name() string  { return c.name }
func (c *deadlineRecordingCommand) Usage() string { return c.name + " - deadline test command" }
func (c *deadlineRecordingCommand) Execute(ctx context.Context, _ []string) (string, error) {
	if _, ok := ctx.Deadline(); ok {
		return "", fmt.Errorf("unexpected deadline")
	}
	return "no deadline", nil
}

func TestBashProxyEnvInjection(t *testing.T) {
	proxy := "socks5://127.0.0.1:1080"
	bash := command.NewBashTool(t.TempDir(), 5, nil).WithScannerProxy(proxy)

	out, err := bash.Execute(context.Background(), bashArgs("env"))
	if err != nil {
		t.Fatalf("bash env: %v", err)
	}

	for _, envVar := range []string{"ALL_PROXY", "all_proxy", "HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		expected := envVar + "=" + proxy
		if !strings.Contains(out, expected) {
			t.Errorf("env output missing %s, got:\n%s", expected, out)
		}
	}
}

func TestBashNoProxyEnvWhenEmpty(t *testing.T) {
	bash := command.NewBashTool(t.TempDir(), 5, nil)

	out, err := bash.Execute(context.Background(), bashArgs("env"))
	if err != nil {
		t.Fatalf("bash env: %v", err)
	}

	if strings.Contains(out, "ALL_PROXY=socks5://") {
		t.Errorf("should not inject proxy env when proxy is empty, got:\n%s", out)
	}
}

func TestBashPseudoCommandBypassesProxyEnv(t *testing.T) {
	registry := command.NewRegistry()
	cmd := newRecordingCommand("gogo")
	registry.Register(cmd, "")

	proxy := "socks5://127.0.0.1:1080"
	bash := command.NewBashTool(t.TempDir(), 5, registry).WithScannerProxy(proxy)

	out, err := bash.Execute(context.Background(), bashArgs("gogo -i 127.0.0.1"))
	if err != nil {
		t.Fatalf("bash gogo: %v", err)
	}
	if !strings.Contains(out, "[gogo] ok") {
		t.Fatalf("expected pseudo-command output, got: %s", out)
	}
}

func bashArgs(cmd string) string {
	data, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		panic(err)
	}
	return string(data)
}
