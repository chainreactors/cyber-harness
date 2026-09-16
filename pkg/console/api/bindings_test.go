package api

import (
	"github.com/spf13/cobra"
	"strings"
	"testing"
)

func TestComposeValidatesCommandsWithoutRunningThem(t *testing.T) {
	calls := 0
	binding := func(name string) *Bindings {
		return &Bindings{Commands: func(View) []*cobra.Command {
			return []*cobra.Command{{Use: name, Run: func(*cobra.Command, []string) { calls++ }}}
		}}
	}
	_, err := Compose(binding("inspect"), binding("inspect"))
	if err == nil || !strings.Contains(err.Error(), `duplicate console command "inspect"`) {
		t.Fatalf("conflict: %v", err)
	}
	if calls != 0 {
		t.Fatal("construction executed command")
	}
	first := binding("first")
	combined, err := Compose(first, binding("second"))
	if err != nil {
		t.Fatal(err)
	}
	first.Commands = nil
	commands := combined.Commands(View{})
	if len(commands) != 2 {
		t.Fatal("source mutation changed composed bindings")
	}
	commands[1].Run(commands[1], nil)
	if calls != 1 {
		t.Fatal("lost command callback")
	}
}
