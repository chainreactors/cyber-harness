package config

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/resource"
	types "github.com/chainreactors/cyber/pkg/types"
)

func TestConnectionUsesTypedResourcePoint(t *testing.T) {
	sections := NewSections()
	resources := resource.New()
	if _, err := resource.Define[Connection](resources, sections.ConnectionPoint()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	handle, err := resource.Add(resources, Connection{Section: "fixture", Test: func(context.Context, *types.DistributeConfig, *types.DistributeConfig) []*types.ConnectionCheck {
		calls++
		return []*types.ConnectionCheck{{Name: "fixture", Ok: true}}
	}})
	if err != nil {
		t.Fatal(err)
	}
	checks, err := sections.TestConnection(t.Context(), "FIXTURE", nil, nil)
	if err != nil || calls != 1 || len(checks) != 1 || !checks[0].Ok {
		t.Fatalf("connection test = %#v, calls=%d, err=%v", checks, calls, err)
	}
	if err := handle.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := sections.TestConnection(t.Context(), "fixture", nil, nil); err == nil {
		t.Fatal("withdrawn connection remained registered")
	}
}

func TestConnectionRejectsDuplicateSection(t *testing.T) {
	sections := NewSections()
	point := sections.ConnectionPoint()
	test := func(context.Context, *types.DistributeConfig, *types.DistributeConfig) []*types.ConnectionCheck {
		return nil
	}
	if _, err := point.Add(Connection{Section: "fixture", Test: test}); err != nil {
		t.Fatal(err)
	}
	if _, err := point.Add(Connection{Section: "fixture", Test: test}); err == nil {
		t.Fatal("duplicate connection was accepted")
	}
}
