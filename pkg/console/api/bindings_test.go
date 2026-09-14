package api

import (
	"github.com/spf13/cobra"
	"strings"
	"testing"
)

func TestComposeValidatesSourcesWithoutRunningCommands(t *testing.T) {
	calls := 0
	binding := func(name string) *Bindings {
		return &Bindings{Commands: func(View) []*cobra.Command {
			return []*cobra.Command{{Use: name, Run: func(*cobra.Command, []string) { calls++ }}}
		}}
	}
	_, err := Compose(Contribution{Source: "one", Bindings: binding("inspect")}, Contribution{Source: "two", Bindings: binding("inspect")})
	if err == nil || !strings.Contains(err.Error(), "one and two") {
		t.Fatalf("conflict: %v", err)
	}
	if calls != 0 {
		t.Fatal("construction executed command")
	}
	first := binding("first")
	combined, err := Compose(Contribution{Source: "one", Bindings: first}, Contribution{Source: "two", Bindings: binding("second")})
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
