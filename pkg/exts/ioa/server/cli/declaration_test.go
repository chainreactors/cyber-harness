package cli

import (
	"context"
	cfg "github.com/chainreactors/aiscan/core/config"
	hostcli "github.com/chainreactors/aiscan/pkg/cli"
	server "github.com/chainreactors/aiscan/pkg/exts/ioa/server"
	flags "github.com/jessevdk/go-flags"
	"testing"
)

func TestServerCommandsWithoutClientDeclaration(t *testing.T) {
	for _, args := range [][]string{{"serve", "--ioa-url", "http://localhost:9000"}, {"ioa", "serve", "--ioa-url", "http://localhost:9000"}} {
		parser := flags.NewNamedParser("server-only", 0)
		r := hostcli.New(parser)
		called := false
		if err := Register(r, func(_ context.Context, value server.Options, _ hostcli.Environment) error {
			called = true
			if value.URL != "http://localhost:9000" {
				t.Fatalf("options = %+v", value)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := parser.ParseArgs(args); err != nil {
			t.Fatal(err)
		}
		if called {
			t.Fatal("parser started server")
		}
		if err := r.Selected().Run(t.Context(), hostcli.Environment{Config: &cfg.Option{Extensions: r.Values()}}); err != nil {
			t.Fatal(err)
		}
		if !called {
			t.Fatal("missing action")
		}
	}
}
