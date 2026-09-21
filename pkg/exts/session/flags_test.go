package session

import (
	"bytes"
	cfg "github.com/chainreactors/cyber/pkg/config"
	flags "github.com/jessevdk/go-flags"
	"strings"
	"testing"
)

func TestFlagsAreInertTypedAndComposable(t *testing.T) {
	var option cfg.AgentOptions
	var extra struct {
		Label string `long:"label"`
	}
	groups := append(FlagGroups(&option), cfg.FlagGroup{Name: "Extra", Options: &extra})
	parser := flags.NewParser(nil, flags.None)
	for _, group := range groups {
		if _, err := parser.AddGroup(group.Name, group.Description, group.Options); err != nil {
			t.Fatal(err)
		}
	}
	var help bytes.Buffer
	parser.WriteHelp(&help)
	if !strings.Contains(help.String(), "resume") || !strings.Contains(help.String(), "label") {
		t.Fatalf("declarations absent from help: %s", help.String())
	}
	if _, err := parser.ParseArgs([]string{"-r", "history.jsonl", "-e", "done", "--label", "local"}); err != nil {
		t.Fatal(err)
	}
	if option.Resume != "history.jsonl" || option.EvalCriteria != "done" || extra.Label != "local" {
		t.Fatalf("typed option binding: %+v %+v", option, extra)
	}
	if option.Timeout != 3600 || option.EvalRounds != "" || option.Transport != "auto" {
		t.Fatalf("defaults changed: %+v", option)
	}
}
