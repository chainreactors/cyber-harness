package api

import (
	"github.com/spf13/cobra"
	"strings"
	"testing"
)

func commandContribution(source, name string, aliases ...string) Contribution {
	return Contribution{Source: source, Bindings: &Bindings{Commands: func(View) []*cobra.Command {
		return []*cobra.Command{{Use: name, Aliases: aliases}}
	}}}
}

func TestRegistryRequiresProviderAndSealsAtPublication(t *testing.T) {
	r := NewRegistry()
	first := commandContribution("session", "/status")
	if err := r.Register(first); err == nil {
		t.Fatal("registered without provider")
	}
	if r.Bindings() != nil {
		t.Fatal("published without provider")
	}
	if err := r.Open(); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(first); err != nil {
		t.Fatal(err)
	}
	first.Bindings.Commands = nil
	b := r.Bindings()
	if got := b.Commands(View{}); len(got) != 1 || got[0].Name() != "/status" {
		t.Fatalf("snapshot changed: %v", got)
	}
	b.Commands = nil
	if r.Bindings().Commands == nil {
		t.Fatal("consumer mutated shared bindings")
	}
	if err := r.Register(commandContribution("late", "/late")); err == nil {
		t.Fatal("registered after publication")
	}
	r.Close()
	if r.Bindings() != nil {
		t.Fatal("published after close")
	}
	if err := r.Open(); err == nil {
		t.Fatal("reopened closed installation")
	}
}

func TestRegistryConflictIsAtomicAndProfilesAreIsolated(t *testing.T) {
	r := NewRegistry()
	if err := r.Open(); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(commandContribution("one", "/inspect", "/i")); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(commandContribution("two", "/other", "/i")); err == nil || !strings.Contains(err.Error(), "one and two") {
		t.Fatalf("conflict: %v", err)
	}
	if err := r.Register(commandContribution("one", "/duplicate-source")); err == nil {
		t.Fatal("duplicate source accepted")
	}
	if err := r.Register(commandContribution("two", "/other")); err != nil {
		t.Fatal(err)
	}
	if got := r.Bindings().Commands(View{}); len(got) != 2 {
		t.Fatalf("partial failed contribution: %v", got)
	}
	other := NewRegistry()
	if err := other.Open(); err != nil {
		t.Fatal(err)
	}
	if err := other.Register(commandContribution("one", "/inspect")); err != nil {
		t.Fatal(err)
	}
	if got := other.Bindings().Commands(View{}); len(got) != 1 {
		t.Fatalf("profile leakage: %v", got)
	}
}
