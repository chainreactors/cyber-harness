package aiscan_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacySessionRuntimeStaysRemoved(t *testing.T) {
	root := repositoryRoot(t)
	legacy := filepath.Join(root, "pkg", "runtime")
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy session runtime must not coexist with exts/agent: %v", err)
	}
	// Runtime data may remain locally, but no second source implementation may.
	legacySources, err := filepath.Glob(filepath.Join(root, "pkg", "exts", "session", "*.go"))
	if err != nil || len(legacySources) != 0 {
		t.Fatalf("session lifecycle must have one owner in exts/agent: %v %v", legacySources, err)
	}
	for _, tree := range []string{"agent", "core", "pkg", "tools", "cmd", "examples"} {
		assertNoImportPrefix(t, filepath.Join(root, tree), modulePath+"/pkg/runtime")
		assertNoImportPrefix(t, filepath.Join(root, tree), modulePath+"/pkg/exts/session")
	}
	for _, path := range []string{"Makefile", filepath.Join(".github", "workflows", "ci.yml")} {
		source := readRepositoryFile(t, root, path)
		if strings.Contains(source, "./core/deps") || strings.Contains(source, "./pkg/runtime") {
			t.Errorf("%s references a removed lifecycle package", path)
		}
	}
}
