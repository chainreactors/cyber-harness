package cli

import (
	"context"
	cfg "github.com/chainreactors/aiscan/core/config"
	hostcli "github.com/chainreactors/aiscan/pkg/cli"
	client "github.com/chainreactors/aiscan/pkg/exts/ioa/client"
	presentation "github.com/chainreactors/aiscan/pkg/exts/ioa/client/console"
	flags "github.com/jessevdk/go-flags"
	"testing"
)

func TestClientDeclarationWithoutAgentOrServer(t *testing.T) {
	for _, test := range []struct {
		args  []string
		valid bool
	}{
		{[]string{"ioa", "spaces", "--json"}, true},
		{[]string{"ioa", "context", "space", "message"}, true},
		{[]string{"ioa", "messages"}, false},
		{[]string{"ioa", "context", "space"}, false},
		{[]string{"ioa", "nodes", "space", "extra"}, false},
	} {
		parser := flags.NewNamedParser("client-only", flags.HelpFlag)
		r := hostcli.New(parser)
		calls := 0
		err := Register(r, func(_ context.Context, mode string, _ client.Options, output presentation.Options, args presentation.Args, _ hostcli.Environment) error {
			calls++
			if mode == "spaces" && !output.JSON {
				t.Fatal("JSON option lost")
			}
			if mode == "context" && (args.Space != "space" || args.MessageID != "message") {
				t.Fatalf("args = %+v", args)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if r.ValueArity()["--ioa-url"] != 1 || r.ValueArity()["--json"] != 0 {
			t.Fatal("declaration arity missing")
		}
		rest, err := parser.ParseArgs(test.args)
		valid := err == nil && len(rest) == 0
		if valid != test.valid {
			t.Fatalf("%v: rest %v, error %v", test.args, rest, err)
		}
		if calls != 0 {
			t.Fatal("parse started client")
		}
		if valid {
			if err := r.Selected().Run(t.Context(), hostcli.Environment{Config: &cfg.Option{Extensions: r.Values()}}); err != nil {
				t.Fatal(err)
			}
		}
	}
}
