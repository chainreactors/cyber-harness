package exts_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The runtime packages must not gain scanner domain types or depend on the
// scanner extension. Protocols and other extension packages may stay specific.
func TestAgentAndCoreRemainScannerNeutral(t *testing.T) {
	root := repositoryRoot(t)
	for _, packageDir := range []string{"agent", "core"} {
		err := filepath.WalkDir(filepath.Join(root, packageDir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if packageDir == "core" && filepath.Dir(path) == filepath.Join(root, "core", "types") && strings.HasSuffix(path, ".pb.go") {
				lower := strings.ToLower(string(source))
				for _, word := range []string{"scanner", "scan", "cyberhub", "recon"} {
					if strings.Contains(lower, word) {
						t.Errorf("%s contains scanner domain %q", path, word)
					}
				}
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
			if err != nil {
				return err
			}
			for _, imp := range file.Imports {
				name, _ := strconv.Unquote(imp.Path.Value)
				if strings.Contains(name, "/scanner") || strings.Contains(name, "/scan") {
					t.Errorf("%s imports scanner domain package %s", path, name)
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				name, ok := node.(*ast.Ident)
				if !ok {
					return true
				}
				value := name.Name
				if strings.HasPrefix(value, "Cyberhub") || strings.HasPrefix(value, "Recon") || (strings.HasPrefix(value, "Scan") && len(value) > 4 && value[4] >= 'A' && value[4] <= 'Z' && value != "ScanJSONL") {
					t.Errorf("%s contains scanner domain identifier %s", path, value)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

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
