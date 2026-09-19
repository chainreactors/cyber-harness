package cli

import (
	"bytes"
	"context"
	cfg "github.com/chainreactors/cyber/core/config"
	flags "github.com/jessevdk/go-flags"
	"strings"
	"testing"
)

func TestThirdExtensionDeclarationsAreInertAndPresenceAware(t *testing.T) {
	parser := flags.NewNamedParser("fixture-host", flags.HelpFlag)
	r := New(parser)
	calls := 0
	if err := r.Command("fixture inspect", "Inspect fixture", &struct{}{}, Action{Run: func(context.Context, Environment) error { calls++; return nil }}); err != nil {
		t.Fatal(err)
	}
	options := &struct {
		Name    string `long:"fixture-name" config:"name"`
		Count   int    `long:"fixture-count" config:"count"`
		Enabled bool   `long:"fixture-enabled" config:"enabled"`
		Token   string `long:"fixture-token" config:"token" default:"sensitive" default-mask:"***"`
	}{}
	if err := r.Group("fixture", "fixture", cfg.FlagGroup{Name: "Fixture", Options: options}); err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseArgs([]string{"fixture", "inspect", "--fixture-name=", "--fixture-count=0"}); err != nil {
		t.Fatal(err)
	}
	var help bytes.Buffer
	parser.WriteHelp(&help)
	if strings.Contains(help.String(), "sensitive") || !strings.Contains(help.String(), "fixture-name") {
		t.Fatalf("invalid help: %s", help.String())
	}
	if calls != 0 {
		t.Fatal("declaration or parsing ran action")
	}
	values := r.Values()["fixture"]
	if len(values) != 2 || values["name"] != "" || values["count"] != 0 {
		t.Fatalf("explicit values = %#v", values)
	}
	if r.Selected() == nil {
		t.Fatal("action missing")
	}
	if err := r.Selected().Run(t.Context(), Environment{}); err != nil || calls != 1 {
		t.Fatalf("run = %v, calls %d", err, calls)
	}
	absent := flags.NewNamedParser("empty", 0)
	rest, _ := absent.ParseArgs([]string{"fixture", "inspect"})
	if len(rest) != 2 || New(absent).Selected() != nil {
		t.Fatal("unregistered command consumed")
	}
}

func TestDuplicateCommandsAndFlagsRejectedBeforeParsing(t *testing.T) {
	r := New(flags.NewNamedParser("host", 0))
	if err := r.Command("fixture", "", &struct{}{}, Action{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Command("fixture", "", &struct{}{}, Action{}); err == nil {
		t.Fatal("duplicate command accepted")
	}
	for i := range 2 {
		err := r.Group("fixture", "fixture", cfg.FlagGroup{Name: "Fixture", Options: &struct {
			Value string `long:"value"`
		}{}})
		if (err != nil) != (i == 1) {
			t.Fatalf("registration %d: %v", i, err)
		}
	}
}

func TestRejectedDeclarationDoesNotPoisonParser(t *testing.T) {
	r := New(flags.NewNamedParser("host", 0))
	group := func() cfg.FlagGroup {
		return cfg.FlagGroup{Name: "Test", Options: &struct {
			Value string `long:"value" config:"value"`
		}{}}
	}
	if err := r.Group("", "first", group()); err != nil {
		t.Fatal(err)
	}
	err := r.Group("", "second", group())
	if err == nil || !strings.Contains(err.Error(), "duplicate flag --value") {
		t.Fatalf("conflict: %v", err)
	}
	if _, err := r.Parse([]string{"--value=kept"}); err != nil {
		t.Fatal(err)
	}
	if got := r.Values(); got["first"]["value"] != "kept" || len(got) != 1 {
		t.Fatalf("residual registration: %#v", got)
	}
	if err := r.Group("", "late", group()); err == nil {
		t.Fatal("registered after parse")
	}
}
func TestRejectedCommandDoesNotLeaveParents(t *testing.T) {
	r := New(flags.NewNamedParser("host", 0))
	err := r.Command("parent broken", "", &struct {
		A string `long:"same"`
		B string `long:"same"`
	}{}, Action{})
	if err == nil {
		t.Fatal("accepted duplicate command flags")
	}
	if r.Parser.Find("parent") != nil {
		t.Fatal("failed declaration left a parent command")
	}
}

func TestTypedContributionsMaterializeAtSealAndCanRetractBeforeIt(t *testing.T) {
	parser := flags.NewNamedParser("host", 0)
	r := New(parser)
	declare := Contribution(func(registry *Registry) error {
		return registry.Command("inspect", "Inspect", &struct{}{}, Action{})
	})
	handle, err := r.Add(declare)
	if err != nil {
		t.Fatal(err)
	}
	if parser.Find("inspect") != nil {
		t.Fatal("contribution materialized before seal")
	}
	if err := handle.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	if parser.Find("inspect") != nil {
		t.Fatal("retracted contribution materialized")
	}

	parser = flags.NewNamedParser("host", 0)
	r = New(parser)
	if _, err := r.Add(declare); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(); err != nil {
		t.Fatal(err)
	}
	if parser.Find("inspect") == nil {
		t.Fatal("sealed contribution was not materialized")
	}
}
