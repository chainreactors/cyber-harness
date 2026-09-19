//go:build full && cgo

package cstx

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
)

func TestExtensionDiscoversArtifactCapabilities(t *testing.T) {
	instance := New()
	set, err := extension.New(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})

	artifacts := instance.ArtifactTypes()
	if len(artifacts) == 0 {
		t.Fatal("CSTX ABI advertised no artifact parsers")
	}
	for _, artifact := range artifacts {
		supported, err := instance.runtime.Extensions.ParsesArtifact(t.Context(), artifact)
		if err != nil {
			t.Fatal(err)
		}
		if !supported {
			t.Fatalf("catalog artifact %q is not parseable after enable", artifact)
		}
	}
}

func TestExtensionRuntimeIsNotAvailableOutsideLoad(t *testing.T) {
	instance := New()
	if instance.runtime != nil || len(instance.ArtifactTypes()) != 0 {
		t.Fatal("new extension unexpectedly owns a runtime")
	}
	set, err := extension.New(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if instance.runtime == nil {
		t.Fatal("loaded extension has no runtime")
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if instance.runtime != nil || len(instance.ArtifactTypes()) != 0 {
		t.Fatal("closed extension retained runtime state")
	}
}
