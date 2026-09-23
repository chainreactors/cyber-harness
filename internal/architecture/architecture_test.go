// Package architecture enforces repository dependency and installation boundaries.
package architecture

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

const module = "github.com/chainreactors/cyber/"

func root(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}

func TestRuntimeDependencies(t *testing.T) {
	repository := root(t)
	// Parse every file, including inactive platform and edition variants.
	err := filepath.WalkDir(repository, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(repository, path)
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative != "." && (strings.HasPrefix(entry.Name(), ".") || relative == "web" || relative == "aop") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		runtime := strings.HasPrefix(relative, "agent/") && !strings.HasSuffix(path, "_test.go")
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if imported == module+"pkg/app" || imported == module+"pkg/exts/app" {
				t.Errorf("%s imports removed App package", relative)
			}
			if !strings.HasSuffix(path, "_test.go") {
				if strings.HasPrefix(relative, "pkg/config/") || strings.HasPrefix(relative, "pkg/cli/configuration/") {
					for _, forbidden := range []string{"pkg/exts/", "cmd/", "tools/"} {
						if strings.HasPrefix(imported, module+forbidden) {
							t.Errorf("%s imports product implementation %s instead of configuration declarations", relative, imported)
						}
					}
				}
				directory := filepath.ToSlash(filepath.Dir(relative))
				if (directory == "agent" || directory == "agent/session") && strings.HasPrefix(imported, module+"agent/subagent") {
					t.Errorf("%s depends on optional subagent capability", relative)
				}
				if strings.HasPrefix(relative, "tools/scan/") && (strings.HasPrefix(imported, module+"agent/subagent") || imported == module+"agent/session") {
					t.Errorf("%s bypasses the injected scanner Worker", relative)
				}
			}
			if !runtime {
				continue
			}
			for _, forbidden := range []string{"pkg/", "tools/", "cmd/", "internal/", "core/extension"} {
				if strings.HasPrefix(imported, module+forbidden) {
					t.Errorf("%s imports %s", relative, imported)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "list", "-deps", "./agent/...")
	cmd.Dir = repository
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dependency graph: %v\n%s", err, out)
	}
	for _, dependency := range strings.Fields(string(out)) {
		for _, layer := range []string{"pkg/", "tools/", "cmd/", "internal/"} {
			if strings.HasPrefix(dependency, module+layer) {
				t.Errorf("Agent transitively depends on %s", dependency)
			}
		}
	}
}

// Each rule identifies the sole production owner. These are source boundaries,
// not a runtime plugin catalog or a second installation mechanism.
type installationRule struct {
	owner   string
	symbols []string
}

var installationRules = map[string][]installationRule{
	"agent/subagent":             {{"pkg/exts/subagent", []string{"NewRegistry"}}},
	"agent/subagent/sessionexec": {{"pkg/exts/subagent", []string{"New"}}},
	"agent/session":              {{"pkg/exts/session", []string{"NewResource", "Resource"}}},
	"agent/provider":             {{"pkg/exts/provider", []string{"Initialize"}}},
	"agent/prompt":               {{"pkg/exts/prompt", []string{"NewRegistry"}}},
	"agent/skills":               {{"pkg/exts/skills", []string{"NewStore", "LoadFrom"}}},
	"tools/ioa":                  {{"pkg/exts/ioa/client/extension.go", []string{"New", "Resource"}}},
	"tools/ioa/server":           {{"pkg/exts/ioa/server/extension.go", []string{"New", "Resource"}}},
	"tools/terminal": {
		{"pkg/exts/terminal", []string{"NewBashTool"}},
		{"pkg/exts/tmux", []string{"NewTmuxCommand"}},
	},
	"tools/files":       {{"pkg/exts/files", []string{"New", "Resource"}}},
	"tools/proxy":       {{"pkg/exts/proxy", []string{"New*", "Resource", "TrafficNamespace"}}},
	"tools/arsenal":     {{"pkg/exts/arsenal", []string{"New*"}}},
	"tools/playwright":  {{"pkg/exts/browser", []string{"New"}}},
	"tools/record":      {{"pkg/exts/record", []string{"New"}}},
	"tools/loop":        {{"pkg/exts/native", []string{"NewCommand"}}},
	"tools/okf":         {{"pkg/exts/okf", []string{"NewCommand"}}},
	"tools/curl":        {{"pkg/exts/scanner", []string{"New*"}}},
	"tools/gogo":        {{"pkg/exts/scanner", []string{"New*"}}},
	"tools/neutron":     {{"pkg/exts/scanner", []string{"New*"}}},
	"tools/proton":      {{"pkg/exts/scanner", []string{"New*"}}},
	"tools/spray":       {{"pkg/exts/scanner", []string{"New*"}}},
	"tools/zombie":      {{"pkg/exts/scanner", []string{"New*"}}},
	"tools/katana":      {{"pkg/exts/scanner", []string{"New*"}}},
	"tools/passive":     {{"pkg/exts/scanner", []string{"New*"}}},
	"tools/scan":        {{"pkg/exts/scanner", []string{"New"}}},
	"tools/scan/engine": {{"pkg/exts/scanner", []string{"InitWithOptions", "NewUncoverEngine"}}},
	"tools/search": {
		{"pkg/exts/scanner", []string{"NewCyberhubSearch"}},
		{"pkg/exts/search", []string{"NewTavilySearch", "NewWebSearchTool", "NewFetchCommand"}},
	},
	"pkg/web/service": {{"pkg/exts/web", []string{"NewService", "NewSQLiteStore", "NewAgentPool"}}},
	"pkg/web":         {{"pkg/exts/web", []string{"ManagementRoutes", "AOPRoute", "SessionRoute", "ScanRoute", "ConfigRoute", "AgentRoute", "SystemRoute", "ArtifactRoute"}}},
}

func installationOwner(imported, symbol string) string {
	for _, rule := range installationRules[strings.TrimPrefix(imported, module)] {
		for _, pattern := range rule.symbols {
			if symbol == pattern || strings.HasSuffix(pattern, "*") && strings.HasPrefix(symbol, strings.TrimSuffix(pattern, "*")) {
				return rule.owner
			}
		}
	}
	return ""
}
func permittedInstallation(relative, imported, owner string) bool {
	directory := filepath.ToSlash(filepath.Dir(relative))
	// Unit tests for the implementation may construct their subject directly.
	implementation := strings.TrimPrefix(imported, module)
	if strings.HasSuffix(relative, "_test.go") {
		return directory == implementation || implementation == "pkg/web" && directory == "pkg/web/service"
	}
	return relative == owner || directory == owner
}
func installationViolations(relative string, file *ast.File) []string {
	var violations []string
	imports := map[string]string{}
	for _, spec := range file.Imports {
		imported, _ := strconv.Unquote(spec.Path.Value)
		name := filepath.Base(imported)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "." && installationRules[strings.TrimPrefix(imported, module)] != nil {
			violations = append(violations, "dot import of owned installation API")
		}
		imports[name] = imported
	}
	ast.Inspect(file, func(node ast.Node) bool {
		var constructed ast.Expr
		if literal, ok := node.(*ast.CompositeLit); ok {
			constructed = literal.Type
		}
		if call, ok := node.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "new" && len(call.Args) == 1 {
				constructed = call.Args[0]
			}
		}
		if typ, ok := constructed.(*ast.SelectorExpr); ok && typ.Sel.Name == "Resource" {
			if qualifier, ok := typ.X.(*ast.Ident); ok {
				imported := imports[qualifier.Name]
				owner := installationOwner(imported, "Resource")
				if owner != "" && !permittedInstallation(relative, imported, owner) {
					violations = append(violations, "resource construction belongs to "+owner)
				}
			}
		}
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if selector.Sel.Name == "NamespaceBindings" && relative != "pkg/exts/session/protocol_extension.go" && !strings.HasPrefix(relative, "agent/session/") {
			violations = append(violations, "Session protocols must be contributed by session.NewProtocol")
		}
		ident, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		imported := imports[ident.Name]
		if selector.Sel.Name == "Resource" {
			return true
		} // Type inspection and borrowing do not install a resource.
		owner := installationOwner(imported, selector.Sel.Name)
		if owner != "" && !permittedInstallation(relative, imported, owner) {
			violations = append(violations, ident.Name+"."+selector.Sel.Name+" belongs to "+owner)
		}
		return true
	})
	return violations
}
func TestExtensionsAreTheInstallationEntryPoints(t *testing.T) {
	repository := root(t)
	for _, tree := range []string{"cmd", "pkg", "examples", "internal/testutil"} {
		err := filepath.WalkDir(filepath.Join(repository, tree), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if strings.HasPrefix(entry.Name(), ".") {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			relative, _ := filepath.Rel(repository, path)
			relative = filepath.ToSlash(relative)
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			for _, violation := range installationViolations(relative, file) {
				t.Errorf("%s: %s", relative, violation)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
func TestInstallationBoundaryRules(t *testing.T) {
	for _, test := range []struct {
		name, path, source string
		reject             bool
	}{
		{"direct", "cmd/example/main.go", `package main; import ioa "github.com/chainreactors/cyber/tools/ioa"; var x = ioa.New`, true},
		{"alias", "examples/main.go", `package main; import original "github.com/chainreactors/cyber/tools/ioa"; var construct = original.New`, true},
		{"anonymous extension", "cmd/example/main.go", `package main; import ioa "github.com/chainreactors/cyber/tools/ioa"; var load = func() { _ = ioa.New(ioa.Config{}, nil) }`, true},
		{"another extension", "pkg/exts/search/extra.go", `package search; import ioa "github.com/chainreactors/cyber/tools/ioa"; var x = ioa.New`, true},
		{"declaration bypass", "pkg/exts/ioa/client/declare.go", `package client; import ioa "github.com/chainreactors/cyber/tools/ioa"; var x = ioa.New`, true},
		{"resource literal", "cmd/example/main.go", `package main; import ioa "github.com/chainreactors/cyber/tools/ioa"; var x = ioa.Resource{}`, true},
		{"protocol alias", "cmd/example/main.go", `package main; func install(runtime R) { bindings := runtime.NamespaceBindings; _ = bindings }`, true},
		{"owner", "pkg/exts/ioa/client/extension.go", `package client; import ioa "github.com/chainreactors/cyber/tools/ioa"; var x = ioa.New`, false},
		{"config", "cmd/example/main.go", `package main; import ioa "github.com/chainreactors/cyber/tools/ioa"; var config ioa.Config`, false},
		{"business", "cmd/example/main.go", `package main; import ioa "github.com/chainreactors/cyber/tools/ioa"; func query(s *ioa.Service) { _ = s.ListSpaces }`, false},
		{"custom extension", "examples/main.go", `package main; import ext "github.com/chainreactors/cyber/core/extension"; var x = ext.Func{}`, false},
		{"unit test", "pkg/web/service/example_test.go", `package service_test; import svc "github.com/chainreactors/cyber/pkg/web/service"; var x = svc.NewService`, false},
		{"integration test", "pkg/exts/web/example_test.go", `package web_test; import svc "github.com/chainreactors/cyber/pkg/web/service"; var x = svc.NewService`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), test.path, test.source, 0)
			if err != nil {
				t.Fatal(err)
			}
			violations := installationViolations(test.path, file)
			if (len(violations) > 0) != test.reject {
				t.Fatalf("violations=%v, reject=%v", violations, test.reject)
			}
		})
	}
}
