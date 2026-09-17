package client

import (
	"github.com/chainreactors/cyber/agent/skills"
	"strings"
	"testing"
)

func TestLoadAllIncludesIOAModuleSkills(t *testing.T) {
	without, _ := skills.LoadAll(nil)
	for _, name := range []string{"ioa", "checkpoint", "handoff", "swarm", "team"} {
		if _, ok := without.ByName(name); ok {
			t.Fatalf("unselected IOA skill %q was loaded", name)
		}
	}
	bundle, _ := Skills()
	store, diags := skills.LoadAll(nil, bundle)
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %#v", diags)
	}
	for _, name := range []string{"checkpoint", "handoff", "swarm", "team"} {
		skill, ok := store.ByName(name)
		if !ok {
			t.Fatalf("missing ioa module skill %q", name)
		}
		if !skill.Internal {
			t.Fatalf("ioa skill %q should be internal", name)
		}
		if body := store.ReadBody(name); body == "" {
			t.Fatalf("ReadBody(%q) returned empty", name)
		}
	}

	content, handled, err := store.ReadVirtual("ioa://skills/checkpoint/SKILL.md")
	if err != nil || !handled {
		t.Fatalf("ReadVirtual(ioa checkpoint) handled=%v err=%v", handled, err)
	}
	if !strings.Contains(content, "name: checkpoint") {
		t.Fatalf("unexpected checkpoint content:\n%s", content)
	}

	schema, handled, err := store.ReadVirtual("ioa://skills/checkpoint/schema.json")
	if err != nil || !handled || !strings.HasPrefix(strings.TrimSpace(schema), "{") {
		t.Fatalf("ReadVirtual(ioa checkpoint schema) handled=%v err=%v", handled, err)
	}
}

func TestIOAFindingConvention(t *testing.T) {
	bundle, _ := Skills()
	store, _ := skills.LoadAll(nil, bundle)
	body, handled, err := store.ReadVirtualBody("cyber://skills/cyber/okf/runtime/ioa-finding.md")
	if err != nil || !handled {
		t.Fatalf("ReadVirtualBody(ioa-finding) handled=%v err=%v", handled, err)
	}
	if !strings.Contains(body, "--kind finding") || !strings.Contains(body, "result_id") {
		t.Fatalf("ioa-finding convention missing checkpoint command or result_id:\n%s", body)
	}
	if strings.Contains(body, "finding-id") {
		t.Fatal("ioa-finding must not reintroduce a separate finding-id")
	}

	main, _, err := store.ReadVirtual("cyber://skills/ioa/SKILL.md")
	if err != nil {
		t.Fatalf("ReadVirtual(SKILL.md) error = %v", err)
	}
	if !strings.Contains(main, "okf/runtime/ioa-finding.md") {
		t.Fatal("SKILL.md does not reference the ioa-finding convention")
	}

	report, _, err := store.ReadVirtual("cyber://skills/cyber/reference/report.md")
	if err != nil {
		t.Fatalf("ReadVirtual(report.md) error = %v", err)
	}
	if !strings.Contains(report, "findings/<result_id>.md") {
		t.Fatal("report.md must name findings by result_id")
	}
}
