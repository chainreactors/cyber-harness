package api

import (
	"github.com/spf13/cobra"
	"strings"
	"testing"
)

func commandBindings(name string, aliases ...string) *Bindings {
	return &Bindings{Commands: func(View) []*cobra.Command {
		return []*cobra.Command{{Use: name, Aliases: aliases}}
	}}
}

func TestRegistrySealsAtPublication(t *testing.T) {
	r := NewRegistry()
	first := commandBindings("/status")
	if _, err := r.Add(first); err != nil {
		t.Fatal(err)
	}
	first.Commands = nil
	b := r.Bindings()
	if got := b.Commands(View{}); len(got) != 1 || got[0].Name() != "/status" {
		t.Fatalf("snapshot changed: %v", got)
	}
	b.Commands = nil
	if r.Bindings().Commands == nil {
		t.Fatal("consumer mutated shared bindings")
	}
	if _, err := r.Add(commandBindings("/late")); err == nil {
		t.Fatal("registered after publication")
	}
}

func TestRegistryConflictIsAtomicAndProfilesAreIsolated(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Add(commandBindings("/inspect", "/i")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add(commandBindings("/other", "/i")); err == nil || !strings.Contains(err.Error(), `duplicate console command "/i"`) {
		t.Fatalf("conflict: %v", err)
	}
	handle, err := r.Add(commandBindings("/other"))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Bindings().Commands(View{}); len(got) != 2 {
		t.Fatalf("partial failed contribution: %v", got)
	}
	if err := handle.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	other := NewRegistry()
	if _, err := other.Add(commandBindings("/inspect")); err != nil {
		t.Fatal(err)
	}
	if got := other.Bindings().Commands(View{}); len(got) != 1 {
		t.Fatalf("profile leakage: %v", got)
	}
}
