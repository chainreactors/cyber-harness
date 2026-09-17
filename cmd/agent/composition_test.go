package main

import (
	"context"
	"errors"
	"testing"

	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/resource"
	"github.com/chainreactors/cyber/core/telemetry"
)

// The minimal build resolves the same capabilities by type that the full one
// does, with a different set of extensions publishing them. This build has no
// model configured, so loading is expected to fail -- but it must fail on the
// provider, never on wiring. ErrTypeUnknown here would mean an extension
// borrows something nothing in this build publishes.
func TestMinimalProfileWiringResolves(t *testing.T) {
	profile, err := newAgentProfile(cfg.Option{}, telemetry.NopLogger(), t.TempDir(), 5)
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	t.Cleanup(func() { _ = profile.Close(context.Background()) })

	err = profile.Load(t.Context())
	if errors.Is(err, resource.ErrTypeUnknown) || errors.Is(err, resource.ErrInvalid) {
		t.Fatalf("the minimal build does not publish everything it borrows: %v", err)
	}
	if errors.Is(err, resource.ErrTypeDefined) {
		t.Fatalf("two extensions claim the same capability: %v", err)
	}
}
