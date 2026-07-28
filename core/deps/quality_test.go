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
		case "node_modules", "dist", "coverage", "playwright-report", "test-results", ".cache":
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
