package skills

import (
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent/skills"
)

func TestBundleProvidesCyberSkill(t *testing.T) {
	bundle, diagnostics := Bundle()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(bundle.Skills) != 1 {
		t.Fatalf("skills = %#v", bundle.Skills)
	}
	skill := bundle.Skills[0]
	if skill.Name != "cyber" || skill.Description == "" {
		t.Fatalf("skill = %#v", skill)
	}
	if skill.Location != "cyber://skills/cyber/SKILL.md" || skill.BaseDir != "cyber://skills/cyber" {
		t.Fatalf("location = %q base = %q", skill.Location, skill.BaseDir)
	}
	if skill.Source != skills.SourceBundle {
		t.Fatalf("source = %q", skill.Source)
	}
}

func TestReadVirtualServesSkillTree(t *testing.T) {
	bundle, _ := Bundle()
	for location, want := range map[string]string{
		"cyber://skills/cyber/SKILL.md":          "# Cyber ASM and Penetration Testing",
		"cyber://skills/cyber/okf/easm/gogo.md":  "# Gogo",
		"cyber://skills/cyber/reference/report.md": "report",
		"cyber://skills/scan/verify.md":          "verify",
	} {
		raw, handled, err := bundle.ReadVirtual(location)
		if err != nil || !handled {
			t.Fatalf("ReadVirtual(%s) handled=%v err=%v", location, handled, err)
		}
		if !strings.Contains(strings.ToLower(raw), strings.ToLower(want)) {
			t.Fatalf("ReadVirtual(%s) missing %q", location, want)
		}
	}

	if _, handled, _ := bundle.ReadVirtual("cyber://skills/cyber/okf/easm/missing.md"); handled {
		t.Fatal("missing file must fall through to later bundles")
	}
	if _, handled, _ := bundle.ReadVirtual("cyber://skills/runtime/tmux.md"); handled {
		t.Fatal("runtime shelf is not owned by this bundle")
	}
	if _, handled, _ := bundle.ReadVirtual("ioa://skills/ioa/SKILL.md"); handled {
		t.Fatal("foreign scheme must not be handled")
	}
}
