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

func bashArgs(cmd string) string {
	data, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		panic(err)
	}
	return string(data)
}
