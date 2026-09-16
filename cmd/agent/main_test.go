package main

import (
	"io"
	"os/exec"
	"strings"
	"testing"

	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
)

func TestAgentCLIExposesOnlyLocalAgentOptions(t *testing.T) {
	parsed, option, err := parseOptions([]string{"--model", "fixture", "--prompt", "inspect", "--workdir", t.TempDir()}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.WorkDir == "" || option.Model != "fixture" || option.Prompt != "inspect" || option.Transport != "local" {
		t.Fatalf("parsed options = %+v, %+v", parsed, option)
	}
	if _, _, err := parseOptions([]string{"--cyberhub-url", "https://example.test"}, io.Discard); err == nil {
		t.Fatal("minimal agent accepted scanner flags")
	}
}

func TestAgentProfileConstructionIsInert(t *testing.T) {
	profile, err := newAgentProfile(cfg.Option{}, telemetry.NopLogger(), t.TempDir(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if profile.extensions.Active() {
		t.Fatal("profile became active during construction")
	}
}

func TestAgentDependencyClosureExcludesProductFeatures(t *testing.T) {
	output, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"/pkg/exts/scanner", "/pkg/exts/search", "/pkg/exts/proxy", "/pkg/exts/ioa",
		"/pkg/exts/browser", "/pkg/exts/record", "/pkg/exts/web", "/pkg/web", "/tools/proxy", "/tools/record",
	}
	for _, dependency := range strings.Fields(string(output)) {
		for _, fragment := range forbidden {
			if strings.Contains(dependency, fragment) {
				t.Errorf("minimal agent links forbidden dependency %s", dependency)
			}
		}
	}
}
