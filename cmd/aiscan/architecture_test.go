package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCoreDoesNotImportApplicationPackages(t *testing.T) {
	root := filepath.Join(repositoryRoot(t), "core")
	fileSet := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			value, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			for _, layer := range []string{"agent", "pkg", "tools", "cmd", "internal"} {
				prefix := "github.com/chainreactors/cyber/" + layer
				if value == prefix || strings.HasPrefix(value, prefix+"/") {
					t.Errorf("core file %s imports application package %s", path, value)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReusableLayersDoNotDependOnDistributionFeatures(t *testing.T) {
	root := repositoryRoot(t)
	cmd := exec.Command("go", "list", "-deps", "./core/...", "./agent/...", "./pkg/harness")
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"/cmd/aiscan", "/pkg/exts/scanner", "/pkg/exts/search", "/pkg/exts/proxy",
		"/pkg/exts/ioa", "/pkg/exts/browser", "/pkg/exts/record", "/pkg/exts/web",
		"/pkg/web", "/pkg/node",
	}
	for _, dependency := range strings.Fields(string(output)) {
		for _, fragment := range forbidden {
			if strings.Contains(dependency, fragment) {
				t.Errorf("reusable layer links distribution dependency %s", dependency)
			}
		}
	}
}

func TestCompositionUsesNoInitRegistration(t *testing.T) {
	root := repositoryRoot(t)
	for _, relative := range []string{"cmd/aiscan"} {
		directory := filepath.Join(root, filepath.FromSlash(relative))
		err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if ok && function.Recv == nil && function.Name.Name == "init" {
					t.Errorf("implicit composition in %s", path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
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
