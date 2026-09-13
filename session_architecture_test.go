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
		t.Fatalf("legacy session runtime must not coexist with exts/session: %v", err)
	}
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
