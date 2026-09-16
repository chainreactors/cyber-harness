package probe

import (
	"context"
	"github.com/chainreactors/cyber/pkg/types"
	"testing"
)

func TestOptionalProbeRegistration(t *testing.T) {
	r := New()
	if _, err := r.Test(t.Context(), "ioa", nil, nil); err == nil {
		t.Fatal("IOA present without registration")
	}
	calls := 0
	check := func(context.Context, *types.DistributeConfig, *types.DistributeConfig) []*types.ConnectionCheck {
		calls++
		return []*types.ConnectionCheck{{Name: "fixture", Ok: true}}
	}
	if _, err := r.Add(Definition{Section: "fixture", Check: check}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("declaration invoked probe")
	}
	if _, err := r.Add(Definition{Section: "fixture", Check: check}); err == nil {
		t.Fatal("duplicate accepted")
	}
	result, err := r.Test(t.Context(), "fixture", nil, nil)
	if err != nil || calls != 1 || !result[0].Ok {
		t.Fatalf("probe = %+v, %v", result, err)
	}
}
