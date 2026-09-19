package hosttest_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	hosttestPath = "github.com/chainreactors/cyber/pkg/hosttest"
	apptestPath  = "github.com/chainreactors/cyber/pkg/apptest"
)

// The two helper packages exist for tests only: they take a testing.TB and call
// t.Fatal. A non-test file that imports either has either shipped test wiring
// into a Profile or is about to recreate the import cycle the split exists to
// prevent. Only apptest may build on hosttest.
func TestOnlyTestsLinkTheHostHelpers(t *testing.T) {
	root := repositoryRoot(t)
	fileSet := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipDirectory(root, path, entry.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			value, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if value != hosttestPath && value != apptestPath {
				continue
			}
			if value == hosttestPath && filepath.ToSlash(filepath.Dir(relative)) == "pkg/apptest" {
				continue
			}
			t.Errorf("non-test file %s imports %s", filepath.ToSlash(relative), value)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// skipDirectory drops vendored trees and the nested modules, which do not share
// this module's import graph.
func skipDirectory(root, path, name string) bool {
	if name == "node_modules" || strings.HasPrefix(name, ".") {
		return true
	}
	if path == root {
		return false
	}
	_, err := os.Stat(filepath.Join(path, "go.mod"))
	return err == nil
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatal("repository root not found")
		}
		current = parent
	}
}
