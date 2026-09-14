package aiscan_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestConsoleConsumesExplicitSessionContributions(t *testing.T) {
	root := repositoryRoot(t)
	source := readRepositoryFile(t, root, filepath.Join("pkg", "console", "interactive.go"))
	if strings.Contains(source, ".CommandSpecs(") {
		t.Fatal("console must consume registered bindings, not discover Session commands itself")
	}
	for _, name := range []string{"session", "ioa/client"} {
		assertNoImportPrefix(t, filepath.Join(root, "pkg", "exts", name, "console"), modulePath+"/pkg/exts/tui")
	}
	assertNoImportPrefix(t, filepath.Join(root, "agent", "session"), modulePath+"/pkg/console")
}
