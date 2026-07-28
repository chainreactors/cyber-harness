package deps_test

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

const modulePath = "github.com/chainreactors/aiscan"

func TestLayerImportsAreUnidirectional(t *testing.T) {
	root := repositoryRoot(t)
	assertNoFirstPartyImports(t, filepath.Join(root, "core"), map[string]bool{
		"agent": true,
		"pkg":   true,
		"tools": true,
		"cmd":   true,
	})
	assertNoFirstPartyImports(t, filepath.Join(root, "agent"), map[string]bool{
		"pkg":   true,
		"tools": true,
		"cmd":   true,
	})
}

func TestLegacyPackagesCannotReturn(t *testing.T) {
	root := repositoryRoot(t)
	legacy := []struct {
		dir        string
		importPath string
	}{
		{dir: filepath.Join("pkg", "agent"), importPath: modulePath + "/pkg/" + "agent"},
		{dir: filepath.Join("pkg", "aop"), importPath: modulePath + "/pkg/" + "aop"},
		{dir: filepath.Join("pkg", "telemetry"), importPath: modulePath + "/pkg/" + "telemetry"},
		{dir: filepath.Join("pkg", "util"), importPath: modulePath + "/pkg/" + "util"},
		{dir: filepath.Join("core", "runner"), importPath: modulePath + "/core/" + "runner"},
		{dir: filepath.Join("core", "transport"), importPath: modulePath + "/core/" + "transport"},
		{dir: filepath.Join("cmd", "agent"), importPath: modulePath + "/cmd/" + "agent"},
	}
	for _, item := range legacy {
		legacyDir := filepath.Join(root, item.dir)
		if _, err := os.Stat(legacyDir); err == nil {
			t.Errorf("legacy package directory still exists: %s", legacyDir)
		} else if !os.IsNotExist(err) {
			t.Errorf("stat legacy package directory: %v", err)
		}
	}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && shouldSkipTree(root, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		imports, parseErr := importsInFile(path)
		if parseErr != nil {
			return parseErr
		}
		for _, importPath := range imports {
			for _, item := range legacy {
				if importPath == item.importPath || strings.HasPrefix(importPath, item.importPath+"/") {
					t.Errorf("legacy import %q in %s", importPath, relative(root, path))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentExampleIsNotMaintainedBuildTarget(t *testing.T) {
	root := repositoryRoot(t)
	example := filepath.Join(root, "examples", "agent", "main.go")
	if _, err := os.Stat(example); err != nil {
		t.Fatalf("agent example is missing: %v", err)
	}

	for _, rel := range []string{
		filepath.Join(".github", "workflows", "ci.yml"),
		"Makefile",
		"build.sh",
	} {
		path := filepath.Join(root, rel)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", relative(root, path), err)
		}
		for _, forbidden := range []string{"cmd/agent", "examples/agent", "aiscan-agent"} {
			if strings.Contains(string(content), forbidden) {
				t.Errorf("maintained build file %s references agent binary token %q", relative(root, path), forbidden)
			}
		}
	}
}

func assertNoFirstPartyImports(t *testing.T, tree string, forbidden map[string]bool) {
	t.Helper()
	root := repositoryRoot(t)
	err := filepath.WalkDir(tree, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		imports, parseErr := importsInFile(path)
		if parseErr != nil {
			return parseErr
		}
		for _, importPath := range imports {
			if !strings.HasPrefix(importPath, modulePath+"/") {
				continue
			}
			remainder := strings.TrimPrefix(importPath, modulePath+"/")
			layer, _, _ := strings.Cut(remainder, "/")
			if forbidden[layer] {
				t.Errorf("forbidden dependency %q in %s", importPath, relative(root, path))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func importsInFile(path string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		imports = append(imports, value)
	}
	return imports, nil
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func shouldSkipTree(root, path string) bool {
	rel := filepath.ToSlash(relative(root, path))
	return rel == ".git" || strings.HasPrefix(rel, ".git/") ||
		rel == "refer" || strings.HasPrefix(rel, "refer/") ||
		rel == "templates" || strings.HasPrefix(rel, "templates/") ||
		rel == "web/frontend/cyber-ui" || strings.HasPrefix(rel, "web/frontend/cyber-ui/")
}

func relative(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}
