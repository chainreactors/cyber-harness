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
		"/cmd/cyber-scan", "/exts/scanner", "/exts/search", "/exts/proxy",
		"/exts/ioa", "/exts/browser", "/exts/record", "/exts/web",
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
	for _, relative := range []string{"cmd/cyber-scan"} {
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

func TestOptionalCapabilitiesFollowCompositionImports(t *testing.T) {
	root := repositoryRoot(t)
	for _, test := range []struct {
		path         string
		want, forbid []string
	}{
		{"./tools/search", nil, []string{"/tools/scan", "/sdk/", "/fingers/", "/neutron/"}},
		{"./tools/passive", nil, []string{"/tools/scan", "/tools/katana", "/tools/playwright"}},
		{"./tools/scan", nil, []string{"/tools/katana", "/tools/headless", "/projectdiscovery/katana"}},
		{"./examples/browser", []string{"/exts/browser"}, []string{"/exts/scanner", "/tools/passive", "/tools/katana"}},
		{"./examples/passive", []string{"/exts/passive"}, []string{"/exts/scanner", "/tools/playwright", "/tools/katana"}},
		{"./examples/crawl", []string{"/exts/katana"}, []string{"/exts/scanner", "/tools/passive", "/tools/playwright"}},
	} {
		t.Run(test.path, func(t *testing.T) {
			cmd := exec.Command("go", "list", "-deps", test.path)
			cmd.Dir = root
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, output)
			}
			for _, want := range test.want {
				if !strings.Contains(string(output), want) {
					t.Errorf("missing %s", want)
				}
			}
			for _, forbid := range test.forbid {
				if strings.Contains(string(output), forbid) {
					t.Errorf("unexpected dependency %s", forbid)
				}
			}
		})
	}
	cmd := exec.Command("go", "list", "-deps", "./cmd/cyber-scan")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	for _, capability := range []string{"/exts/browser", "/exts/passive", "/exts/katana"} {
		if !strings.Contains(string(output), "github.com/chainreactors/cyber"+capability+"\n") {
			t.Errorf("capability %s membership does not match composition", capability)
		}
	}
	cmd = exec.Command("go", "list", "-deps", ".")
	cmd.Dir = filepath.Join(root, "cmd", "audit")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("audit dependencies: %v: %s", err, output)
	}
	for _, capability := range []string{"/exts/scanner", "/tools/scan", "/tools/passive", "/tools/playwright", "/tools/katana"} {
		if strings.Contains(string(output), capability) {
			t.Errorf("audit links %s", capability)
		}
	}
}
