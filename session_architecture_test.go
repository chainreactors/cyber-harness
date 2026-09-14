package aiscan_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentLoopAndSessionManagementHaveDistinctOwners(t *testing.T) {
	root := repositoryRoot(t)
	legacy := filepath.Join(root, "pkg", "runtime")
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy session runtime must stay removed: %v", err)
	}

	agentSource := readRepositoryFile(t, root, filepath.Join("pkg", "exts", "agent", "extension.go"))
	for _, required := range []string{
		"func New(loop coreagent.Loop)",
		"func (r *Runtime) Run(",
		"var _ coreagent.Loop = (*Runtime)(nil)",
	} {
		if !strings.Contains(agentSource, required) {
			t.Errorf("agent extension does not own the loop boundary: missing %q", required)
		}
	}
	for _, forbidden := range []string{"Session", "OpenSession", "EnsureSession", "CommandSpec", "Application", "IOA"} {
		if strings.Contains(agentSource, forbidden) {
			t.Errorf("agent extension retains session-management concern %q", forbidden)
		}
	}

	sessionSource := readRepositoryFile(t, root, filepath.Join("pkg", "exts", "session", "extension.go"))
	for _, required := range []string{
		"func New(config Config)",
		"func (e *Extension) Runtime() *Runtime",
		"return e.runtime.load(scope)",
		"return e.runtime.close(ctx)",
	} {
		if !strings.Contains(sessionSource, required) {
			t.Errorf("session extension does not own session management: missing %q", required)
		}
	}
	if strings.Contains(sessionSource, "func (r *Runtime) Run(") {
		t.Fatal("session extension reimplements the agent loop boundary")
	}

	assertNoImportPrefix(t, filepath.Join(root, "pkg", "exts", "session"), modulePath+"/pkg/exts/agent")
	for _, tree := range []string{"agent", "core", "pkg", "tools", "cmd", "examples"} {
		assertNoImportPrefix(t, filepath.Join(root, tree), modulePath+"/pkg/runtime")
	}
	for _, path := range []string{"Makefile", filepath.Join(".github", "workflows", "ci.yml")} {
		source := readRepositoryFile(t, root, path)
		if strings.Contains(source, "./core/deps") || strings.Contains(source, "./pkg/runtime") {
			t.Errorf("%s references a removed lifecycle package", path)
		}
	}
}
