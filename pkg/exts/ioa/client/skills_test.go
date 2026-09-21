package client

import (
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	promptext "github.com/chainreactors/cyber/pkg/exts/prompt"
	skillsext "github.com/chainreactors/cyber/pkg/exts/skills"
	ioatools "github.com/chainreactors/cyber/tools/ioa"

	"strings"
	"testing"
)

func TestLoadAllIncludesIOAModuleSkills(t *testing.T) {
	without := installedSkills(t, false)
	for _, name := range []string{"ioa", "checkpoint", "handoff", "swarm", "team"} {
		if _, ok := without.ByName(name); ok {
			t.Fatalf("unselected IOA skill %q was loaded", name)
		}
	}
	store := installedSkills(t, true)
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
	store := installedSkills(t, true)
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

func installedSkills(t *testing.T, collaboration bool) *skills.Store {
	t.Helper()
	library, err := skillsext.NewLibrary(skillsext.LibraryConfig{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	values := []extension.Extension{hosttest.Capabilities(), library, promptext.New()}
	if collaboration {
		bundle, diagnostics := Skills()
		if len(diagnostics) != 0 {
			t.Fatal(diagnostics)
		}
		values = append(values, New(ioatools.Config{}), NewCollaboration(CollaborationOptions{Skills: []skills.Bundle{bundle}}))
	}
	hosttest.Load(t, t.Context(), values...)
	return library.Store()
}
