package capability

import (
	"reflect"
	"testing"
)

func TestRegisterFirstWinsAndRecordsConflict(t *testing.T) {
	reset()
	Register(Descriptor{ID: "gogo", Kind: KindScanner, CLIName: "gogo", Summary: "gogo"})
	Register(Descriptor{ID: "gogo", Kind: KindScanner, CLIName: "shadow"})

	d, ok := Get("gogo")
	if !ok || d.CLIName != "gogo" {
		t.Fatalf("first registration should win, got %#v (ok=%v)", d, ok)
	}
	if got := Conflicts(); len(got) != 1 || got[0].ID != "gogo" {
		t.Fatalf("conflicts = %#v, want one entry for gogo", got)
	}
}

func TestGroupDefaultsToID(t *testing.T) {
	reset()
	Register(Descriptor{ID: "arsenal"})
	d, _ := Get("arsenal")
	if d.Group != "arsenal" {
		t.Fatalf("group = %q, want arsenal", d.Group)
	}
}

func TestQueriesOnlySeeLinkedCapabilities(t *testing.T) {
	reset()
	Register(Descriptor{
		ID: "gogo", Kind: KindScanner, Group: "scanner",
		CLIName: "gogo", Summary: "gogo", UsageLine: "  gogo   Run gogo directly",
		Usage: func() string { return "gogo help" },
	})

	if !CLIAvailable("gogo") {
		t.Fatal("gogo should be CLI-available")
	}
	if CLIAvailable("katana") {
		t.Fatal("katana is not linked and must not be CLI-available")
	}
	if got := Summaries(); !reflect.DeepEqual(got, []string{"gogo"}) {
		t.Fatalf("summaries = %#v", got)
	}
	if got := UsageLines(); !reflect.DeepEqual(got, []string{"  gogo   Run gogo directly"}) {
		t.Fatalf("usage lines = %#v", got)
	}
	if usage, ok := Usage("gogo"); !ok || usage != "gogo help" {
		t.Fatalf("usage = %q ok=%v", usage, ok)
	}
	if _, ok := Usage("katana"); ok {
		t.Fatal("unlinked capability must not render usage")
	}
}

func TestSkillGatingFollowsLinkedCapability(t *testing.T) {
	reset()
	if SkillEnabled("katana") {
		t.Fatal("katana skill must stay hidden while the capability is unlinked")
	}
	if !SkillEnabled("scan") {
		t.Fatal("ungated skills are always enabled")
	}
	Register(Descriptor{ID: "katana", Kind: KindScanner, Skills: []string{"katana"}})
	if !SkillEnabled("katana") {
		t.Fatal("katana skill should unlock once the capability is linked")
	}
}

func TestSelectHonoursOptionalAndDefault(t *testing.T) {
	reset()
	Register(Descriptor{ID: "core"})
	Register(Descriptor{ID: "search", Optional: true, Default: true})
	Register(Descriptor{ID: "browser", Optional: true, Default: true})
	Register(Descriptor{ID: "ioa", Optional: true})

	plan := Select(Options{})
	for _, id := range []ID{"core", "search", "browser"} {
		if !plan.Has(id) {
			t.Fatalf("%s should be enabled by default", id)
		}
	}
	if plan.Has("ioa") {
		t.Fatal("non-default optional capability must stay off")
	}

	plan = Select(Options{OptionalTools: []string{"browser"}})
	if plan.Has("search") {
		t.Fatal("explicit --tools must not keep other optional capabilities")
	}
	if !plan.Has("browser") || !plan.Has("core") {
		t.Fatal("explicit --tools must keep the selection and all non-optional capabilities")
	}

	plan = Select(Options{Extra: []ID{"ioa"}})
	if !plan.Has("ioa") {
		t.Fatal("Extra must force-enable a capability")
	}
}

func TestPlanGroupsFollowRegistrationOrder(t *testing.T) {
	reset()
	Register(Descriptor{ID: "core", Group: "core"})
	Register(Descriptor{ID: "gogo", Group: "scanner"})
	Register(Descriptor{ID: "spray", Group: "scanner"})
	Register(Descriptor{ID: "arsenal", Group: "arsenal"})

	if got := Select(Options{}).Groups(); !reflect.DeepEqual(got, []string{"core", "scanner", "arsenal"}) {
		t.Fatalf("groups = %#v", got)
	}
}
