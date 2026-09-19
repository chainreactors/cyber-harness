package exts_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every feature under pkg/exts is mounted as a whole, so its subtree must
// declare at least one Extension. A subtree without one is either glue that
// belongs to the feature owning the resource, or a feature that skipped the
// adapter layer and made the host learn about it instead.
func TestEveryExtensionSubtreeDeclaresAnExtension(t *testing.T) {
	root := filepath.Join(repositoryRoot(t), "pkg", "exts")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !declaresExtension(t, filepath.Join(root, entry.Name())) {
			t.Errorf("pkg/exts/%s declares no Extension", entry.Name())
		}
	}
}

func declaresExtension(t *testing.T, root string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" || strings.HasPrefix(entry.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && isLoadMethod(function) {
				found = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func isLoadMethod(function *ast.FuncDecl) bool {
	if function.Recv == nil || function.Name.Name != "Load" || function.Type.Params == nil {
		return false
	}
	params := function.Type.Params.List
	if len(params) != 1 {
		return false
	}
	pointer, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := pointer.X.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "Scope"
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
