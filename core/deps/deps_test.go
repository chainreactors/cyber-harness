package deps_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
		{dir: filepath.Join("pkg", "web", "terminal"), importPath: modulePath + "/pkg/web/terminal"},
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

func TestPTYWireDoesNotUseLegacyUtilsProtocol(t *testing.T) {
	root := repositoryRoot(t)
	legacyImport := "github.com/chainreactors/utils/pty"
	legacySelectors := map[string]bool{"Frame": true, "Router": true, "NewRouter": true}
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
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		aliases := make(map[string]bool)
		for _, spec := range file.Imports {
			importPath, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			if importPath != legacyImport {
				continue
			}
			alias := "pty"
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			if alias == "." {
				t.Errorf("legacy PTY runtime must not be dot-imported in %s", relative(root, path))
				continue
			}
			aliases[alias] = true
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok || !legacySelectors[selector.Sel.Name] {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if ok && aliases[pkg.Name] {
				t.Errorf("legacy PTY wire symbol %s.%s in %s", pkg.Name, selector.Sel.Name, relative(root, path))
			}
			return true
		})
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

func TestAIScanProtocolPackagesStayFlat(t *testing.T) {
	root := repositoryRoot(t)
	for _, rel := range []string{
		filepath.Join("proto", "types"), filepath.Join("proto", "rpc"),
		filepath.Join("pkg", "types"), filepath.Join("pkg", "rpc"),
	} {
		dir := filepath.Join(root, rel)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", relative(root, dir), err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				t.Errorf("AIScan protocol package must stay flat: %s", filepath.ToSlash(filepath.Join(rel, entry.Name())))
			}
		}
	}
	rpcDir := filepath.Join(root, "pkg", "rpc")
	entries, err := os.ReadDir(rpcDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".pb.go") && !strings.HasSuffix(entry.Name(), ".connect.go") {
			t.Errorf("pkg/rpc must contain generated bindings only: %s", entry.Name())
		}
	}
	legacy := filepath.Join(root, "proto", "aiscan")
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy nested proto root still exists: %s", relative(root, legacy))
	}
}

func TestWebManagementAPIBoundary(t *testing.T) {
	root := repositoryRoot(t)
	apiTree := filepath.Join(root, "pkg", "web", "api")
	for _, forbidden := range []string{
		"connectrpc.com/connect",
		modulePath + "/pkg/rpc",
		modulePath + "/pkg/web",
	} {
		assertNoImportPrefix(t, apiTree, forbidden)
	}

	entries, err := os.ReadDir(filepath.Join(root, "pkg", "web"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		if strings.Contains(entry.Name(), "_connect") {
			t.Errorf("Connect exposure must stay consolidated in pkg/web/connect.go: %s", entry.Name())
		}
		if filepath.Ext(entry.Name()) != ".go" || entry.Name() == "connect.go" {
			continue
		}
		path := filepath.Join(root, "pkg", "web", entry.Name())
		imports, parseErr := importsInFile(path)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, importPath := range imports {
			if importPath == "connectrpc.com/connect" || importPath == modulePath+"/pkg/rpc" {
				t.Errorf("generated RPC exposure escaped pkg/web/connect.go: %s imports %q", entry.Name(), importPath)
			}
		}
	}

	aopService, err := os.ReadFile(filepath.Join(root, "proto", "rpc", "aop.proto"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"service AOPService", "rpc Connect(stream .aop.Envelope) returns (stream .aop.Envelope)"} {
		if !strings.Contains(string(aopService), required) {
			t.Errorf("proto/rpc/aop.proto is missing %q", required)
		}
	}
}

func TestGoTestFilesFollowSourceFiles(t *testing.T) {
	root := repositoryRoot(t)
	allowedSuffixes := map[string]bool{
		"default": true, "e2e": true, "full": true, "integration": true,
		"native": true, "unix": true, "windows": true,
	}
	sources := make(map[string][]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && shouldSkipQualityTree(root, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir := filepath.Dir(path)
		base := strings.TrimSuffix(filepath.Base(path), ".go")
		sources[dir] = append(sources[dir], base)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && shouldSkipQualityTree(root, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		base := strings.TrimSuffix(filepath.Base(path), "_test.go")
		for _, source := range sources[filepath.Dir(path)] {
			if base == source {
				return nil
			}
			prefix := source + "_"
			if !strings.HasPrefix(base, prefix) {
				continue
			}
			valid := true
			for _, suffix := range strings.Split(strings.TrimPrefix(base, prefix), "_") {
				if !allowedSuffixes[suffix] {
					valid = false
					break
				}
			}
			if valid {
				content, readErr := os.ReadFile(path)
				if readErr != nil {
					return readErr
				}
				if !strings.HasPrefix(string(content), "//go:build ") {
					t.Errorf("additional test file must be isolated by a build tag: %s", relative(root, path))
				}
				return nil
			}
		}
		t.Errorf("test file has no matching source file: %s", relative(root, path))
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
	path := filepath.Join(root, "pkg", "web", "service", "broker.go")
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
			return err
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

type skipAllowance struct {
	Path     string `json:"path"`
	Format   string `json:"format"`
	Count    int    `json:"count"`
	Category string `json:"category"`
	Reason   string `json:"reason"`
}

type skipKey struct {
	Path   string
	Format string
}

func TestSkipsMatchCentralRegistry(t *testing.T) {
	root := repositoryRoot(t)
	registryPath := filepath.Join(root, "test-skips.json")
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}

	var allowances []skipAllowance
	if err := json.Unmarshal(data, &allowances); err != nil {
		t.Fatalf("parse %s: %v", relative(root, registryPath), err)
	}

	allowedCategories := map[string]bool{
		"capability":       true,
		"external_api":     true,
		"external_runtime": true,
		"live_llm":         true,
		"platform":         true,
	}
	want := make(map[skipKey]int, len(allowances))
	for _, allowance := range allowances {
		key := skipKey{Path: filepath.ToSlash(allowance.Path), Format: allowance.Format}
		switch {
		case key.Path == "" || key.Format == "":
			t.Errorf("skip registry entry must include path and format: %+v", allowance)
		case allowance.Count <= 0:
			t.Errorf("skip registry entry must have a positive count: %+v", allowance)
		case !allowedCategories[allowance.Category]:
			t.Errorf("skip registry entry has invalid category %q: %+v", allowance.Category, allowance)
		case strings.TrimSpace(allowance.Reason) == "":
			t.Errorf("skip registry entry must document its reason: %+v", allowance)
		case want[key] != 0:
			t.Errorf("duplicate skip registry entry for %s %q", key.Path, key.Format)
		default:
			want[key] = allowance.Count
		}
	}

	got, scanErrors := scanSkipCalls(root)
	for _, scanErr := range scanErrors {
		t.Error(scanErr)
	}

	keys := make([]skipKey, 0, len(want)+len(got))
	seen := make(map[skipKey]bool, len(want)+len(got))
	for key := range want {
		seen[key] = true
		keys = append(keys, key)
	}
	for key := range got {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Path == keys[j].Path {
			return keys[i].Format < keys[j].Format
		}
		return keys[i].Path < keys[j].Path
	})
	for _, key := range keys {
		if got[key] != want[key] {
			t.Errorf("skip registry mismatch for %s %q: found %d, registered %d", key.Path, key.Format, got[key], want[key])
		}
	}
}

func scanSkipCalls(root string) (map[skipKey]int, []error) {
	got := make(map[skipKey]int)
	var scanErrors []error
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && shouldSkipQualityTree(root, path) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
			calls, scriptErrors := scriptSkipCalls(root, path)
			for key, count := range calls {
				got[key] += count
			}
			scanErrors = append(scanErrors, scriptErrors...)
			return nil
		}
		if ext != ".go" {
			return nil
		}

		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "Skip" && selector.Sel.Name != "Skipf" && selector.Sel.Name != "SkipNow") {
				return true
			}
			receiver, ok := selector.X.(*ast.Ident)
			if !ok || (receiver.Name != "t" && receiver.Name != "b") {
				return true
			}
			if len(call.Args) == 0 {
				scanErrors = append(scanErrors, fmt.Errorf("unregistered reasonless skip in %s", relative(root, path)))
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				scanErrors = append(scanErrors, fmt.Errorf("skip reason must be a string literal in %s", relative(root, path)))
				return true
			}
			format, unquoteErr := strconv.Unquote(literal.Value)
			if unquoteErr != nil {
				scanErrors = append(scanErrors, fmt.Errorf("parse skip reason in %s: %w", relative(root, path), unquoteErr))
				return true
			}
			got[skipKey{Path: relative(root, path), Format: format}]++
			return true
		})
		return nil
	})
	if err != nil {
		scanErrors = append(scanErrors, err)
	}
	return got, scanErrors
}

var (
	scriptSkipStart  = regexp.MustCompile(`\b(?:test|it|describe)\.skip\s*\(`)
	scriptSkipReason = regexp.MustCompile("'[^']*'|\"[^\"]*\"|`[^`]*`")
)

func scriptSkipCalls(root, path string) (map[skipKey]int, []error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, []error{err}
	}
	rel := relative(root, path)
	got := make(map[skipKey]int)
	var scanErrors []error
	for lineNumber, line := range strings.Split(string(data), "\n") {
		starts := scriptSkipStart.FindAllStringIndex(line, -1)
		for i, start := range starts {
			end := len(line)
			if i+1 < len(starts) {
				end = starts[i+1][0]
			}
			literals := scriptSkipReason.FindAllString(line[start[0]:end], -1)
			if len(literals) == 0 {
				scanErrors = append(scanErrors, fmt.Errorf("skip reason must be a string literal in %s:%d", rel, lineNumber+1))
				continue
			}
			literal := literals[len(literals)-1]
			reason := literal[1 : len(literal)-1]
			got[skipKey{Path: rel, Format: reason}]++
		}
	}
	return got, scanErrors
}

func TestRepositoryDebtMarkersCannotReturn(t *testing.T) {
	root := repositoryRoot(t)
	markers := [][]byte{[]byte("TO" + "DO"), []byte("FIX" + "ME")}
	var failures []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && shouldSkipQualityTree(root, path) {
				return filepath.SkipDir
			}
			return nil
		}

		rel := relative(root, path)
		if isBackupFile(entry.Name()) {
			failures = append(failures, rel+": backup/editor artifact")
			return nil
		}
		if !isDebtScannable(path) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, marker := range markers {
			if bytes.Contains(data, marker) {
				failures = append(failures, fmt.Sprintf("%s: contains forbidden debt marker %q", rel, marker))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(failures)
	for _, failure := range failures {
		t.Error(failure)
	}
}

func shouldSkipQualityTree(root, path string) bool {
	if shouldSkipTree(root, path) {
		return true
	}
	rel := filepath.ToSlash(relative(root, path))
	if rel == "web/static" || strings.HasPrefix(rel, "web/static/") {
		return true
	}
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		switch part {
		case "node_modules", "dist", "coverage", "playwright-report", "test-results", ".cache", ".tmp":
			return true
		}
	}
	return false
}

func isBackupFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, "~") ||
		strings.HasSuffix(lower, ".bak") ||
		strings.HasSuffix(lower, ".backup") ||
		strings.HasSuffix(lower, ".orig") ||
		strings.HasSuffix(lower, ".rej") ||
		strings.HasSuffix(lower, ".swp") ||
		strings.HasSuffix(lower, ".swo") ||
		strings.HasPrefix(lower, ".#")
}

func isDebtScannable(path string) bool {
	base := filepath.Base(path)
	if base == "Makefile" || base == "Dockerfile" || base == ".gitattributes" || base == ".gitmodules" {
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".css", ".go", ".html", ".js", ".json", ".jsx", ".md", ".mod", ".ps1", ".scss", ".sh", ".sum", ".toml", ".ts", ".tsx", ".yaml", ".yml":
		return true
	default:
		return false
	}
}
