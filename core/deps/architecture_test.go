package deps_test

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

const modulePath = "github.com/chainreactors/aiscan"

func TestLayerImportsAreUnidirectional(t *testing.T) {
	root := repositoryRoot(t)
	assertNoFirstPartyImports(t, filepath.Join(root, "core"), map[string]bool{
		"agent": true,
		"tools": true,
		"cmd":   true,
	})
	assertNoFirstPartyImports(t, filepath.Join(root, "agent"), map[string]bool{
		"tools": true,
		"cmd":   true,
	})
	assertNoPkgImportsExceptTypes(t, filepath.Join(root, "core"))
	assertNoPkgImportsExceptTypes(t, filepath.Join(root, "agent"))
}

func TestAOPProtocolLayerHasNoRuntimeDependencies(t *testing.T) {
	root := repositoryRoot(t)
	assertNoFirstPartyImports(t, filepath.Join(root, "aop"), map[string]bool{
		"agent": true,
		"core":  true,
		"pkg":   true,
		"tools": true,
		"cmd":   true,
	})
}

func TestRunnerDoesNotDependOnWeb(t *testing.T) {
	root := repositoryRoot(t)
	assertNoImportPrefix(t, filepath.Join(root, "pkg", "runner"), modulePath+"/pkg/web")
	assertNoImportPrefix(t, filepath.Join(root, "pkg", "runner"), modulePath+"/pkg/rpc")
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
		{dir: filepath.Join("cmd", "runner"), importPath: modulePath + "/cmd/" + "runner"},
		{dir: filepath.Join("core", "aop"), importPath: modulePath + "/core/aop"},
		{dir: filepath.Join("pkg", "webproto"), importPath: modulePath + "/pkg/webproto"},
		{dir: filepath.Join("pkg", "webagent"), importPath: modulePath + "/pkg/webagent"},
		{dir: filepath.Join("pkg", "web", "proto"), importPath: modulePath + "/pkg/web/proto"},
		{dir: filepath.Join("internal", "aoputil"), importPath: modulePath + "/internal/aoputil"},
		{dir: filepath.Join("internal", "gen"), importPath: modulePath + "/internal/gen"},
		{dir: "api", importPath: modulePath + "/api"},
		{dir: filepath.Join("aop", "ext"), importPath: modulePath + "/aop/ext"},
		{dir: filepath.Join("aop", "aiscan"), importPath: modulePath + "/aop/aiscan"},
	}
	for _, item := range legacy {
		legacyDir := filepath.Join(root, item.dir)
		if hasGoFiles(legacyDir) {
			t.Errorf("legacy package still contains Go files: %s", legacyDir)
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

func TestGeneratedProtobufLivesInOwnedProtocolTrees(t *testing.T) {
	root := repositoryRoot(t)
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
		name := entry.Name()
		if !strings.HasSuffix(name, ".pb.go") && !strings.HasSuffix(name, ".connect.go") {
			return nil
		}
		rel := filepath.ToSlash(relative(root, path))
		if !strings.HasPrefix(rel, "aop/") && !strings.HasPrefix(rel, "pkg/types/") && !strings.HasPrefix(rel, "pkg/rpc/") {
			t.Errorf("generated protobuf file outside owned protocol trees: %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSharedTypesDoNotDependOnRPCOrConnect(t *testing.T) {
	root := repositoryRoot(t)
	tree := filepath.Join(root, "pkg", "types")
	for _, forbidden := range []string{modulePath + "/pkg/rpc", modulePath + "/pkg/web", "connectrpc.com/connect"} {
		assertNoImportPrefix(t, tree, forbidden)
	}
}

func TestWebProtocolDoesNotDefineGenericJSONEnvelope(t *testing.T) {
	root := repositoryRoot(t)
	tree := filepath.Join(root, "pkg", "web")
	err := filepath.WalkDir(tree, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(node ast.Node) bool {
			typeSpec, ok := node.(*ast.TypeSpec)
			if !ok {
				return true
			}
			structure, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				return true
			}
			hasTypeString, hasRawPayload := false, false
			for _, field := range structure.Fields.List {
				for _, name := range field.Names {
					if name.Name == "Type" && expressionName(field.Type) == "string" {
						hasTypeString = true
					}
					if (name.Name == "Data" || name.Name == "Payload" || name.Name == "Value" || name.Name == "Body") && expressionName(field.Type) == "json.RawMessage" {
						hasRawPayload = true
					}
				}
			}
			if hasTypeString && hasRawPayload {
				t.Errorf("generic Type + json.RawMessage envelope %s in %s", typeSpec.Name.Name, relative(root, path))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLiveBrokerDoesNotUseJSON(t *testing.T) {
	root := repositoryRoot(t)
	path := filepath.Join(root, "pkg", "web", "broker.go")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"encoding/json", "json.RawMessage", "protojson"} {
		if strings.Contains(string(content), forbidden) {
			t.Errorf("live broker contains JSON bridge %q", forbidden)
		}
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

func assertNoImportPrefix(t *testing.T, tree, forbidden string) {
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
			if importPath == forbidden || strings.HasPrefix(importPath, forbidden+"/") {
				t.Errorf("forbidden dependency %q in %s", importPath, relative(root, path))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertNoPkgImportsExceptTypes(t *testing.T, tree string) {
	t.Helper()
	root := repositoryRoot(t)
	prefix := modulePath + "/pkg/"
	allowed := modulePath + "/pkg/types"
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
			if strings.HasPrefix(importPath, prefix) && importPath != allowed && !strings.HasPrefix(importPath, allowed+"/") {
				t.Errorf("forbidden pkg dependency %q in %s", importPath, relative(root, path))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func hasGoFiles(tree string) bool {
	found := false
	_ = filepath.WalkDir(tree, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() && filepath.Ext(path) == ".go" {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

func expressionName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return expressionName(value.X) + "." + value.Sel.Name
	default:
		return ""
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
