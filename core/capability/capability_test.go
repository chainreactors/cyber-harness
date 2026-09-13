package capability

import (
	"reflect"
	"testing"
)

func TestCatalogRejectsAmbiguousIdentity(t *testing.T) {
	if _, err := New(Descriptor{ID: "gogo"}, Descriptor{ID: "gogo"}); err == nil {
		t.Fatal("duplicate ID was accepted")
	}
	if _, err := New(
		Descriptor{ID: "gogo", CLIName: "scan"},
		Descriptor{ID: "spray", CLIName: "scan"},
	); err == nil {
		t.Fatal("duplicate CLI command was accepted")
	}
}

func TestCatalogIsExplicitAndImmutable(t *testing.T) {
	skills := []string{"katana"}
	catalog := Must(Descriptor{
		ID: "katana", Kind: KindScanner, Group: "scanner", CLIName: "katana",
		Summary: "katana", UsageLine: "  katana   crawl", Usage: func() string { return "katana help" }, Skills: skills,
	})
	skills[0] = "changed"
	all := catalog.All()
	all[0].Skills[0] = "also changed"
	descriptor, ok := catalog.Get("katana")
	if !ok || !reflect.DeepEqual(descriptor.Skills, []string{"katana"}) {
		t.Fatalf("catalog changed through caller-owned data: %#v", descriptor)
	}
	if !catalog.CLIAvailable("katana") || catalog.CLIAvailable("passive") {
		t.Fatal("CLI discovery did not follow the explicit catalog")
	}
	if usage, ok := catalog.Usage("katana"); !ok || usage != "katana help" {
		t.Fatalf("usage = %q, %v", usage, ok)
	}
	if !catalog.SkillEnabled("katana") || catalog.SkillEnabled("passive") {
		t.Fatal("skill visibility did not follow the explicit catalog")
	}
}

func TestSelectHonoursOptionalAndDefault(t *testing.T) {
	catalog := Must(
		Descriptor{ID: "core"},
		Descriptor{ID: "search", Optional: true, Default: true},
		Descriptor{ID: "browser", Optional: true, Default: true},
		Descriptor{ID: "ioa", Optional: true},
	)
	plan := catalog.Select(Options{})
	for _, id := range []ID{"core", "search", "browser"} {
		if !plan.Has(id) {
			t.Fatalf("%s should be enabled by default", id)
		}
	}
	if plan.Has("ioa") {
		t.Fatal("non-default optional capability was enabled")
	}
	plan = catalog.Select(Options{OptionalTools: []string{"browser"}})
	if plan.Has("search") || !plan.Has("browser") || !plan.Has("core") {
		t.Fatal("explicit optional selection was not respected")
	}
	plan = catalog.Select(Options{Extra: []ID{"ioa"}})
	if !plan.Has("ioa") {
		t.Fatal("extra capability was not enabled")
	}
}

func TestPlanGroupsFollowDescriptorOrder(t *testing.T) {
	catalog := Must(
		Descriptor{ID: "core", Group: "core"},
		Descriptor{ID: "gogo", Group: "scanner"},
		Descriptor{ID: "spray", Group: "scanner"},
		Descriptor{ID: "arsenal", Group: "arsenal"},
	)
	if got := catalog.Select(Options{}).Groups(); !reflect.DeepEqual(got, []string{"core", "scanner", "arsenal"}) {
		t.Fatalf("groups = %#v", got)
	}
}
