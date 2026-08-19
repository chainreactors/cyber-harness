package repositorytest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
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

func TestRunnerIsSingleTagFreeImplementation(t *testing.T) {
	root := repositoryRoot(t)
	for _, rel := range []string{filepath.Join("pkg", "runner"), filepath.Join("cmd", "runner")} {
		dir := filepath.Join(root, rel)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
				continue
			}
			content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(string(content), "//go:build ") {
				t.Errorf("runner source must not use build tags: %s", filepath.Join(rel, entry.Name()))
			}
		}
	}

	runnerDir := filepath.Join(root, "pkg", "runner")
	entries, err := os.ReadDir(runnerDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source := strings.TrimSuffix(entry.Name(), "_test.go") + ".go"
		if _, err := os.Stat(filepath.Join(runnerDir, source)); err != nil {
			t.Errorf("runner test must map to exactly one source file: %s", entry.Name())
		}
	}

	makefile := readRepositoryFile(t, root, "Makefile")
	start := strings.Index(makefile, "runner: prepare\n")
	if start < 0 {
		t.Fatal("Makefile missing runner target")
	}
	block := makefile[start:]
	if next := strings.Index(block, "\n\n"); next >= 0 {
		block = block[:next]
	}
	if !strings.Contains(block, "./cmd/runner") {
		t.Error("Makefile runner target must build ./cmd/runner")
	}
	if strings.Contains(block, "-tags") {
		t.Error("Makefile runner target must not use build tags")
	}

	releaseWorkflow := readRepositoryFile(t, root, filepath.Join(".github", "workflows", "go-release.yml"))
	if count := strings.Count(releaseWorkflow, "main: ./cmd/runner"); count != 1 {
		t.Fatalf("release workflow must contain exactly one runner build, got %d", count)
	}
	runnerStart := strings.Index(releaseWorkflow, "          - id: runner\n")
	if runnerStart < 0 {
		t.Fatal("release workflow is missing the runner build")
	}
	runnerBuild := releaseWorkflow[runnerStart:]
	if next := strings.Index(runnerBuild[1:], "\n          - id:"); next >= 0 {
		runnerBuild = runnerBuild[:next+1]
	}
	for _, required := range []string{
		"profile: runner",
		"main: ./cmd/runner",
		"binary: runner",
		"tags: \"\"",
		"linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64",
	} {
		if !strings.Contains(runnerBuild, required) {
			t.Errorf("runner release build is missing %q", required)
		}
	}

	goreleaser := readRepositoryFile(t, root, ".goreleaser.yml")
	if count := strings.Count(goreleaser, "main: ./cmd/runner"); count != 1 {
		t.Fatalf("GoReleaser must contain exactly one runner build, got %d", count)
	}
	runnerStart = strings.Index(goreleaser, "  - id: runner\n")
	if runnerStart < 0 {
		t.Fatal("GoReleaser is missing the runner build")
	}
	runnerBuild = goreleaser[runnerStart:]
	if next := strings.Index(runnerBuild[1:], "\n  - id:"); next >= 0 {
		runnerBuild = runnerBuild[:next+1]
	}
	if strings.Contains(runnerBuild, "\n    tags:") {
		t.Error("GoReleaser runner build must not use build tags")
	}
}

func TestGeneratedProtobufLivesInOwnedProtocolTrees(t *testing.T) {
	root := repositoryRoot(t)
	for _, rel := range trackedFiles(t, root) {
		name := filepath.Base(rel)
		if !strings.HasSuffix(name, ".pb.go") && !strings.HasSuffix(name, ".connect.go") {
			continue
		}
		if !strings.HasPrefix(rel, "aop/") && !strings.HasPrefix(rel, "pkg/types/") && !strings.HasPrefix(rel, "pkg/rpc/") {
			t.Errorf("generated protobuf file outside owned protocol trees: %s", rel)
		}
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
	files := trackedFiles(t, root)
	for _, rel := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			continue
		}
		dir := filepath.Dir(path)
		base := strings.TrimSuffix(filepath.Base(path), ".go")
		sources[dir] = append(sources[dir], base)
	}

	for _, rel := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if !strings.HasSuffix(path, "_test.go") {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(path), "_test.go")
		_, exactErr := os.Stat(filepath.Join(filepath.Dir(path), base+".go"))
		matched := exactErr == nil
		for _, source := range sources[filepath.Dir(path)] {
			if base == source {
				matched = true
				break
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
					t.Fatal(readErr)
				}
				if !strings.HasPrefix(string(content), "//go:build ") {
					t.Errorf("additional test file must be isolated by a build tag: %s", relative(root, path))
				}
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("test file has no matching source file: %s", relative(root, path))
		}
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

func TestBuildProfilesUseExpectedCGOModes(t *testing.T) {
	root := repositoryRoot(t)

	makefile := readRepositoryFile(t, root, "Makefile")
	for _, required := range []string{
		"GO_LDFLAGS ?= -s -w",
		"standard: prepare\n\tCGO_ENABLED=0 $(GO) build $(BUILD_FLAGS) -ldflags \"$(GO_LDFLAGS)\"",
		"full: frontend prepare\n\tCGO_ENABLED=1 $(GO) build $(BUILD_FLAGS) -ldflags \"$(GO_LDFLAGS)\"",
		"record: frontend record-native prepare\n\t$(RECORD_BUILD_ENV) CGO_ENABLED=1 $(GO) build $(BUILD_FLAGS) -ldflags \"$(GO_LDFLAGS)\"",
		"STANDARD_TAGS := forceposix emptytemplates noembed osusergo netgo",
		"FULL_TAGS := forceposix emptytemplates noembed osusergo netgo full sqlite re2_cgo re2_static",
		"RECORD_TAGS := $(FULL_TAGS) record_ffmpeg",
	} {
		if !strings.Contains(makefile, required) {
			t.Errorf("Makefile missing build profile contract %q", required)
		}
	}
	buildScript := readRepositoryFile(t, root, "build.sh")
	for _, required := range []string{
		"CGO_MODE=0",
		"CGO_MODE=1",
		`EXTRA_TAGS="full,re2_cgo,re2_static${EXTRA_TAGS:+,$EXTRA_TAGS}"`,
		`CGO_ENABLED="$CGO_MODE"`,
		`OSARCH="${HOST_OS}/${HOST_ARCH}"`,
	} {
		if !strings.Contains(buildScript, required) {
			t.Errorf("build.sh missing build profile contract %q", required)
		}
	}
	for name, profile := range map[string]string{"Makefile full profile": makefile, "build.sh full profile": buildScript} {
		if strings.Contains(profile, "full,record_ffmpeg") || strings.Contains(profile, "full: frontend record-native") {
			t.Errorf("%s must not enable the optional recorder", name)
		}
	}
	releaseWorkflow := readRepositoryFile(t, root, filepath.Join(".github", "workflows", "go-release.yml"))
	for _, forbidden := range []string{"record_ffmpeg", "matrix.recorder"} {
		if strings.Contains(releaseWorkflow, forbidden) {
			t.Errorf("release workflow must not enable the optional recorder; found %q", forbidden)
		}
	}

	goreleaser := readRepositoryFile(t, root, ".goreleaser.yml")
	fullStart := strings.Index(goreleaser, "  - id: aiscan-full\n")
	if fullStart < 0 {
		t.Fatal(".goreleaser.yml missing aiscan-full build")
	}
	fullConfig := goreleaser[fullStart:]
	if next := strings.Index(fullConfig[1:], "\n  - id:"); next >= 0 {
		fullConfig = fullConfig[:next+1]
	}
	if !strings.Contains(fullConfig, "CGO_ENABLED=1") {
		t.Error(".goreleaser.yml aiscan-full must enable CGO")
	}
	if !strings.Contains(fullConfig, "      - darwin\n") {
		t.Error(".goreleaser.yml aiscan-full must publish Darwin builds")
	}
	for _, tag := range []string{"re2_cgo", "re2_static"} {
		if !strings.Contains(fullConfig, "      - "+tag+"\n") {
			t.Errorf(".goreleaser.yml aiscan-full missing build tag %q", tag)
		}
	}
}

func TestGitHubActionsCrossCompileDarwinWithoutMacOSRunners(t *testing.T) {
	root := repositoryRoot(t)
	workflowDir := filepath.Join(root, ".github", "workflows")
	macOSRunner := regexp.MustCompile(`(?i)^(?:runs-on|runner):\s*macos(?:-|\s|$)`)
	err := filepath.WalkDir(workflowDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || (filepath.Ext(path) != ".yml" && filepath.Ext(path) != ".yaml") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for lineNumber, line := range strings.Split(string(content), "\n") {
			if macOSRunner.MatchString(strings.TrimSpace(line)) {
				t.Errorf("macOS GitHub Actions runner in %s:%d", relative(root, path), lineNumber+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	releaseWorkflow := readRepositoryFile(t, root, filepath.Join(".github", "workflows", "go-release.yml"))
	standardStart := strings.Index(releaseWorkflow, "          - id: aiscan\n")
	if standardStart < 0 {
		t.Fatal("release workflow is missing the standard aiscan build")
	}
	standardConfig := releaseWorkflow[standardStart:]
	if next := strings.Index(standardConfig[1:], "\n          - id:"); next >= 0 {
		standardConfig = standardConfig[:next+1]
	}
	for _, required := range []string{
		"runner: ubuntu-22.04",
		"darwin/amd64",
		"darwin/arm64",
		`cgo: "0"`,
	} {
		if !strings.Contains(standardConfig, required) {
			t.Errorf("standard release build must cross-compile Darwin on Linux; missing %q", required)
		}
	}

	fullDarwinStart := strings.Index(releaseWorkflow, "          - id: aiscan-full-darwin\n")
	if fullDarwinStart < 0 {
		t.Fatal("release workflow is missing the full Darwin cross-build")
	}
	fullDarwinConfig := releaseWorkflow[fullDarwinStart:]
	if next := strings.Index(fullDarwinConfig[1:], "\n          - id:"); next >= 0 {
		fullDarwinConfig = fullDarwinConfig[:next+1]
	}
	for _, required := range []string{
		"runner: ubuntu-22.04",
		"darwin/amd64",
		"darwin/arm64",
		`cgo: "1"`,
		"cross: darwin",
		"re2_cgo",
		"re2_static",
	} {
		if !strings.Contains(fullDarwinConfig, required) {
			t.Errorf("full release build must cross-compile Darwin CGO binaries on Linux; missing %q", required)
		}
	}
	if strings.Contains(fullDarwinConfig, "record_ffmpeg") {
		t.Error("full Darwin cross-build must not enable the unsupported native recorder")
	}
	versions := readRepositoryFile(t, root, filepath.Join(".github", "native", "versions.env"))
	for _, required := range []string{
		"MACOS_CROSS_ZIG_VERSION=",
		"MACOS_CROSS_SDK_VERSION=",
		"MACOS_CROSS_SDK_SHA256=",
		"MACOS_CROSS_DEPLOYMENT_TARGET=",
	} {
		if !strings.Contains(versions, required) {
			t.Errorf("native versions file is missing macOS cross-build pin %q", required)
		}
	}
}

func TestRecorderNativeBuildUsesSingleSDKScript(t *testing.T) {
	root := repositoryRoot(t)
	obsoleteScripts := []string{
		"build-" + "linux.sh",
		"build-" + "windows.sh",
		"fetch" + ".sh",
		"package" + ".sh",
		"verify-ffmpeg-" + "config.sh",
	}
	makefile := readRepositoryFile(t, root, "Makefile")
	for _, required := range []string{
		"record-native:",
		"record-native-source:",
		"record-native-package:",
		"MINGW% MSYS% CYGWIN%",
		`"$(BASH)" ".github/native/sdk.sh" fetch`,
		`"$(BASH)" ".github/native/sdk.sh" build`,
		`"$(BASH)" ".github/native/sdk.sh" package`,
	} {
		if !strings.Contains(makefile, required) {
			t.Errorf("Makefile missing recorder build contract %q", required)
		}
	}

	for _, rel := range []string{
		"Makefile",
		"build.sh",
		filepath.Join(".github", "workflows", "ci.yml"),
		filepath.Join(".github", "workflows", "go-release.yml"),
		filepath.Join(".github", "workflows", "record-native.yml"),
	} {
		content := readRepositoryFile(t, root, rel)
		for _, obsolete := range obsoleteScripts {
			if strings.Contains(content, obsolete) {
				t.Errorf("%s still references removed recorder script %q", rel, obsolete)
			}
		}
	}
	for _, obsolete := range obsoleteScripts {
		path := filepath.Join(root, ".github", "native", obsolete)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("removed recorder script still exists: %s", relative(root, path))
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", relative(root, path), err)
		}
	}

	sdk := readRepositoryFile(t, root, filepath.Join(".github", "native", "sdk.sh"))
	for _, command := range []string{"fetch|build|env)", "package)"} {
		if !strings.Contains(sdk, command) {
			t.Errorf("recorder SDK script missing command dispatch %q", command)
		}
	}
}

func readRepositoryFile(t *testing.T, root, rel string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return strings.ReplaceAll(string(content), "\r\n", "\n")
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
	for {
		if _, statErr := os.Stat(filepath.Join(wd, "go.mod")); statErr == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatal("repository root not found")
		}
		wd = parent
	}
}

func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "ls-files", "-z")
	data, err := cmd.Output()
	if err != nil {
		t.Skipf("repository governance requires a Git checkout: %v", err)
	}
	var files []string
	for _, raw := range bytes.Split(data, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		rel := filepath.ToSlash(string(raw))
		info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if statErr != nil || info.IsDir() {
			continue
		}
		files = append(files, rel)
	}
	return files
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

	got, scanErrors := scanSkipCalls(t, root)
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

func scanSkipCalls(t *testing.T, root string) (map[skipKey]int, []error) {
	got := make(map[skipKey]int)
	var scanErrors []error
	for _, rel := range trackedFiles(t, root) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
			calls, scriptErrors := scriptSkipCalls(root, path)
			for key, count := range calls {
				got[key] += count
			}
			scanErrors = append(scanErrors, scriptErrors...)
			continue
		}
		if ext != ".go" {
			continue
		}

		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			scanErrors = append(scanErrors, parseErr)
			continue
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
	for _, rel := range trackedFiles(t, root) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if isBackupFile(filepath.Base(path)) {
			failures = append(failures, rel+": backup/editor artifact")
			continue
		}
		if !isDebtScannable(path) {
			continue
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, marker := range markers {
			if bytes.Contains(data, marker) {
				failures = append(failures, fmt.Sprintf("%s: contains forbidden debt marker %q", rel, marker))
			}
		}
	}
	sort.Strings(failures)
	for _, failure := range failures {
		t.Error(failure)
	}
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
