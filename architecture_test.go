package aiscan_test

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

func TestExtensionGraphIsIndependentOfProductCatalog(t *testing.T) {
	assertNoImportPrefix(t, filepath.Join(repositoryRoot(t), "core", "extension"), modulePath)
}

func TestConfigAPIHasNoProfileOwnership(t *testing.T) {
	dir := filepath.Join(repositoryRoot(t), "pkg", "web", "api")
	for _, forbidden := range []string{"pkg/profile", "pkg/app", "pkg/runtime", "pkg/web/service", "core/extension"} {
		assertNoImportPrefix(t, dir, modulePath+"/"+forbidden)
	}
}

func TestToolsDoNotDependOnHostsOrPresentation(t *testing.T) {
	root := repositoryRoot(t)
	for _, forbidden := range []string{"core/extension", "pkg/app", "pkg/runtime", "pkg/console", "pkg/exts", "pkg/host", "pkg/runner", "pkg/tui", "pkg/web", "pkg/node", "cmd"} {
		assertNoImportPrefix(t, filepath.Join(root, "tools"), modulePath+"/"+forbidden)
	}
}

func TestAppBusinessLayerHasNoRuntimeOrPresentationDependencies(t *testing.T) {
	root := repositoryRoot(t)
	for _, forbidden := range []string{"pkg/runtime", "pkg/console", "pkg/host", "pkg/runner", "pkg/tui", "pkg/web", "pkg/node", "cmd"} {
		assertNoImportPrefix(t, filepath.Join(root, "pkg", "app"), modulePath+"/"+forbidden)
	}
	assertNoImportPrefix(t, filepath.Join(root, "pkg", "web", "api"), modulePath+"/pkg/runner")
	assertNoImportPrefix(t, filepath.Join(root, "pkg", "web", "service"), modulePath+"/pkg/runner")
}

func TestCommunicationHostOnlyDependsOnProtocol(t *testing.T) {
	root := repositoryRoot(t)
	dir := filepath.Join(root, "pkg", "host")
	assertNoFirstPartyImports(t, dir, map[string]bool{
		"agent": true, "core": true, "pkg": true, "tools": true, "cmd": true, "skills": true,
	})
}

func TestConsoleHasNoLegacyTUIBoundary(t *testing.T) {
	root := repositoryRoot(t)
	for _, tree := range []string{"pkg", "cmd"} {
		assertNoImportPrefix(t, filepath.Join(root, tree), modulePath+"/pkg/tui")
	}
	entries, err := os.ReadDir(filepath.Join(root, "pkg", "tui"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("legacy TUI package must be removed")
	}
}

func TestAgentRuntimeDoesNotImportPresentation(t *testing.T) {
	root := repositoryRoot(t)
	for _, forbidden := range []string{"pkg/runner", "pkg/console", "pkg/tui", "pkg/host", "pkg/node", "pkg/web", "cmd"} {
		assertNoImportPrefix(t, filepath.Join(root, "pkg", "exts", "agent"), modulePath+"/"+forbidden)
	}
	assertNoImportPrefix(t, filepath.Join(root, "pkg", "runner"), modulePath+"/pkg/tui")
	assertNoImportPrefix(t, filepath.Join(root, "pkg", "console"), modulePath+"/pkg/runner")
	assertNoImportPrefix(t, filepath.Join(root, "pkg", "node"), modulePath+"/pkg/runner")
	assertNoImportPrefix(t, filepath.Join(root, "pkg", "web"), modulePath+"/pkg/tui")
}

func TestAgentRuntimeDependencyClosureIsHeadless(t *testing.T) {
	root := repositoryRoot(t)
	cmd := exec.Command("go", "list", "-deps", "./pkg/exts/agent")
	cmd.Dir = root
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("runtime dependencies: %v\n%s", err, data)
	}
	for _, dep := range strings.Fields(string(data)) {
		for _, forbidden := range []string{"pkg/runner", "pkg/console", "pkg/tui", "pkg/host", "pkg/node", "pkg/web", "cmd"} {
			prefix := modulePath + "/" + forbidden
			if dep == prefix || strings.HasPrefix(dep, prefix+"/") {
				t.Errorf("runtime transitively depends on %s", dep)
			}
		}
	}
}

func TestAgentFreeToolSurfaceHasNoProductDependencies(t *testing.T) {
	root := repositoryRoot(t)
	packages := []string{
		"./core/registry",
		"./pkg/toolset",
		"./tools/files",
		"./pkg/exts/files",
		"./pkg/toolnode",
	}
	for _, pkg := range packages {
		t.Run(strings.TrimPrefix(pkg, "./"), func(t *testing.T) {
			cmd := exec.Command("go", "list", "-deps", pkg)
			cmd.Dir = root
			data, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("dependencies: %v\n%s", err, data)
			}
			for _, dep := range strings.Fields(string(data)) {
				for _, forbidden := range []string{"agent", "pkg/app", "pkg/runtime", "pkg/console", "pkg/host", "pkg/runner", "pkg/web", "pkg/node", "cmd"} {
					prefix := modulePath + "/" + forbidden
					if dep == prefix || strings.HasPrefix(dep, prefix+"/") {
						t.Errorf("transitively depends on %s", dep)
					}
				}
			}
		})
	}
}

func TestToolAndCommandRegistriesShareOnlyTheLifecycleKernel(t *testing.T) {
	root := repositoryRoot(t)
	commandRegistry := readRepositoryFile(t, root, filepath.Join("pkg", "commands", "registry.go"))
	toolRegistry := readRepositoryFile(t, root, filepath.Join("pkg", "toolset", "registry.go"))
	for path, source := range map[string]string{
		"pkg/commands/registry.go": commandRegistry,
		"pkg/toolset/registry.go":  toolRegistry,
	} {
		if !strings.Contains(source, modulePath+"/core/registry") {
			t.Errorf("%s must use the shared registry lifecycle kernel", path)
		}
	}
	assertNoImportPrefix(t, filepath.Join(root, "pkg", "commands"), modulePath+"/pkg/toolset")
	assertNoImportPrefix(t, filepath.Join(root, "pkg", "toolset"), modulePath+"/pkg/commands")
	assertNoImportPrefix(t, filepath.Join(root, "core", "registry"), modulePath+"/core/extension")
}

func TestExtensionOwnershipBoundariesAreStructural(t *testing.T) {
	root := repositoryRoot(t)
	stream := readRepositoryFile(t, root, filepath.Join("core", "events", "stream.go"))
	if strings.Contains(stream, "Bus *eventbus.Bus") {
		t.Fatal("canonical event stream exposes its writable bus")
	}
	for _, rel := range []string{
		filepath.Join("pkg", "exts", "files", "extension.go"),
		filepath.Join("pkg", "exts", "ioa", "client", "extension.go"),
		filepath.Join("pkg", "exts", "ioa", "server", "extension.go"),
		filepath.Join("pkg", "exts", "proxy", "extension.go"),
	} {
		source := readRepositoryFile(t, root, rel)
		if strings.Contains(source, "struct {\n\t*") {
			t.Errorf("extension embeds and exposes its owned resource: %s", filepath.ToSlash(rel))
		}
	}
	resources := map[string][]string{
		filepath.Join("tools", "files", "fs.go"):              {"type Resource struct {\n\tFiles *Files", "func (r *Resource) Open", "func (r *Resource) Close"},
		filepath.Join("tools", "ioa", "service.go"):           {"type Resource struct {\n\tRuntime *Runtime", "func (r *Resource) Start", "func (r *Resource) Close"},
		filepath.Join("tools", "proxy", "hub.go"):             {"type Resource struct {\n\tProxyHub *ProxyHub", "func (r *Resource) Start", "func (r *Resource) Close"},
		filepath.Join("pkg", "app", "app.go"):                 {"type Resource struct {\n\tApp *App", "func (r *Resource) Load", "func (r *Resource) Close"},
		filepath.Join("pkg", "exts", "agent", "extension.go"): {"type Extension struct{ runtime *Runtime }", "func (e *Extension) Load", "func (e *Extension) Close"},
	}
	for rel, required := range resources {
		source := readRepositoryFile(t, root, rel)
		for _, value := range required {
			if !strings.Contains(source, value) {
				t.Errorf("resource/capability split is missing: %s missing %q", filepath.ToSlash(rel), value)
			}
		}
	}
	for rel, forbidden := range map[string][]string{
		filepath.Join("tools", "files", "fs.go"):            {"func (f *Files) Open", "func (f *Files) Close"},
		filepath.Join("tools", "ioa", "service.go"):         {"func (m *Runtime) Start", "func (m *Runtime) Close"},
		filepath.Join("tools", "proxy", "hub.go"):           {"func (h *ProxyHub) Start", "func (h *ProxyHub) Close"},
		filepath.Join("pkg", "app", "app.go"):               {"func (a *App) Load(", "func (a *App) Close("},
		filepath.Join("pkg", "exts", "agent", "runtime.go"): {"func (rt *Runtime) Load(", "func (rt *Runtime) Close("},
	} {
		source := readRepositoryFile(t, root, rel)
		for _, value := range forbidden {
			if strings.Contains(source, value) {
				t.Errorf("business capability owns lifecycle: %s contains %q", filepath.ToSlash(rel), value)
			}
		}
	}
	agentExtension := readRepositoryFile(t, root, filepath.Join("pkg", "exts", "agent", "extension.go"))
	agentRuntime := readRepositoryFile(t, root, filepath.Join("pkg", "exts", "agent", "runtime.go"))
	for source, required := range map[string][]string{
		agentExtension: {"type Extension struct{ runtime *Runtime }", "func (e *Extension) Runtime() *Runtime", "func (rt *Runtime) Run"},
		agentRuntime:   {"type Runtime struct"},
	} {
		for _, value := range required {
			if strings.Contains(source, value) {
				continue
			}
			t.Errorf("agent lifecycle/capability split is missing %q", value)
		}
	}
	if strings.Contains(agentExtension, "func (e *Extension) Run(") {
		t.Fatal("agent lifecycle extension duplicates the runtime execution API")
	}
	if strings.Contains(agentExtension, "func (e *Extension) Loop(") {
		t.Fatal("agent lifecycle extension aliases its single Runtime capability")
	}
	for _, method := range []string{"OpenSession", "EnsureSession", "Observe", "RunSession"} {
		if strings.Contains(agentExtension, "func (e *Extension) "+method) {
			t.Errorf("agent lifecycle owner publishes business method %q", method)
		}
	}
	legacySources, err := filepath.Glob(filepath.Join(root, "pkg", "exts", "session", "*.go"))
	if err != nil || len(legacySources) != 0 {
		t.Fatal("session remains a second extension boundary instead of the agent runtime capability")
	}
	skillsExtension := readRepositoryFile(t, root, filepath.Join("pkg", "exts", "skills", "extension.go"))
	for _, required := range []string{"type Catalog struct", "func (m *Extension) Catalog() *Catalog", "func (c *Catalog) Locations"} {
		if !strings.Contains(skillsExtension, required) {
			t.Errorf("skills lifecycle/catalog split is missing %q", required)
		}
	}
	if strings.Contains(skillsExtension, "func (m *Extension) Locations") {
		t.Fatal("skills lifecycle extension publishes catalog operations directly")
	}
	profileSource := readRepositoryFile(t, root, filepath.Join("cmd", "aiscan", "profile_aiscan.go"))
	if strings.Count(profileSource, "agentext.New(") != 1 {
		t.Fatal("AIScan profile must construct one Agent lifecycle extension")
	}
	if strings.Contains(profileSource, "*proxyext.Extension") || !strings.Contains(profileSource, "proxytool.RegisterTrafficNamespace(mux, p.proxy)") {
		t.Fatal("AIScan profile must publish the proxy capability without retaining its lifecycle extension")
	}
	workspaceProfile := readRepositoryFile(t, root, filepath.Join("cmd", "runner", "profile_workspace.go"))
	if strings.Contains(workspaceProfile, "*skillmount.Extension") {
		t.Fatal("workspace profile retains the skills lifecycle owner as its catalog")
	}
	extensionScope := readRepositoryFile(t, root, filepath.Join("core", "extension", "scope.go"))
	for _, obsolete := range []string{"type Context struct", "ContextFor", "func (s *Scope) Owner", "ownerSequence", "type Dispose func"} {
		if strings.Contains(extensionScope, obsolete) {
			t.Errorf("extension scope retains obsolete identity indirection %q", obsolete)
		}
	}
	operationSource := readRepositoryFile(t, root, filepath.Join("core", "operation", "operation.go"))
	if strings.Contains(operationSource, "RefFromContext") {
		t.Fatal("operation correlation is exposed as an ambiguous context Ref")
	}
	for _, rel := range []string{
		filepath.Join("tools", "files", "mount.go"),
		filepath.Join("tools", "ioa", "service.go"),
		filepath.Join("tools", "proxy", "config.go"),
	} {
		if source := readRepositoryFile(t, root, rel); strings.Contains(source, "func Borrow") {
			t.Errorf("capability is still fabricated through Borrow: %s", filepath.ToSlash(rel))
		}
	}
	extRoot := filepath.Join(root, "pkg", "exts")
	entries, err := os.ReadDir(extRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			// A feature may organize its own CLI, configuration and presentation
			// adapters in subpackages; dependencies on other features stay forbidden.
			assertNoImportPrefix(t, filepath.Join(extRoot, entry.Name()), modulePath+"/pkg/exts", modulePath+"/pkg/exts/"+entry.Name())
		}
	}
	forbidden := "extension.ErrCloseIncomplete"
	err = filepath.WalkDir(extRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if bytes.Contains(content, []byte(forbidden)) {
			t.Errorf("extension duplicates Set cleanup classification: %s", relative(root, path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAIScanProfileIsOnlyApplicationCompositionRoot(t *testing.T) {
	root := repositoryRoot(t)
	assertNoImportPrefix(t, filepath.Join(root, "pkg", "app"), modulePath+"/pkg/exts")
	err := filepath.WalkDir(filepath.Join(root, "pkg", "app"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, forbidden := range []string{
			"extension.New(", "*extension.Set", "func (a *App) Entries(",
			"buildExtensions(", "editionToolEntries(", "editionExtensionEntries(",
		} {
			if bytes.Contains(content, []byte(forbidden)) {
				t.Errorf("App must not own a nested lifecycle graph: %s contains %q", relative(root, path), forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := readRepositoryFile(t, root, filepath.Join("cmd", "aiscan", "profile_application.go"))
	for _, required := range []string{"newApplicationGraph(", "func (a *applicationGraph) entriesFor("} {
		if !strings.Contains(profile, required) {
			t.Errorf("AIScan Profile is not the application composition root: missing %q", required)
		}
	}
	abstraction := readRepositoryFile(t, root, filepath.Join("pkg", "profile", "profile.go"))
	for _, forbidden := range []string{"proxyext", "observeext", "eventoutput", "newAIScanProfile"} {
		if strings.Contains(abstraction, forbidden) {
			t.Errorf("generic profile assembler contains product implementation %q", forbidden)
		}
	}
}

func TestProfileDelegatesLifecycleToExtensionSet(t *testing.T) {
	root := repositoryRoot(t)
	profileRoot := filepath.Join(root, "pkg", "profile")
	err := filepath.WalkDir(profileRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if filepath.Dir(path) != profileRoot {
			t.Errorf("product profile implementation escaped its command composition root: %s", relative(root, path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	abstraction := readRepositoryFile(t, root, filepath.Join("pkg", "profile", "profile.go"))
	for _, obsolete := range []string{"type Profile struct", "type Config struct", "RegisterResourceNamespaces func", "sync.Mutex", "sync.RWMutex"} {
		if strings.Contains(abstraction, obsolete) {
			t.Errorf("profile duplicates lifecycle or adds a parallel host abstraction: %q", obsolete)
		}
	}
	for _, required := range []string{"type Application interface", "type Factory func"} {
		if !strings.Contains(abstraction, required) {
			t.Errorf("product profile contract is missing %q", required)
		}
	}
	assertNoImportPrefix(t, filepath.Join(root, "pkg", "profile"), modulePath+"/core/extension")
	extensionSource := readRepositoryFile(t, root, filepath.Join("core", "extension", "extension.go"))
	if !strings.Contains(extensionSource, "func (s *Set) Active() bool") {
		t.Fatal("extension.Set does not own graph publication state")
	}

	for _, rel := range []string{
		filepath.Join("cmd", "aiscan", "profile_aiscan.go"),
		filepath.Join("cmd", "runner", "profile_files.go"),
		filepath.Join("cmd", "runner", "profile_workspace.go"),
	} {
		source := readRepositoryFile(t, root, rel)
		if !strings.Contains(source, "*extension.Set") || !strings.Contains(source, "extension.New(") {
			t.Errorf("standalone command profile must own one core extension.Set: %s", filepath.ToSlash(rel))
		}
		for _, duplicate := range []string{"sync.Mutex", "sync.RWMutex", "active bool", "closing bool"} {
			if strings.Contains(source, duplicate) {
				t.Errorf("command profile duplicates Set state: %s contains %q", filepath.ToSlash(rel), duplicate)
			}
		}
	}
}

func TestTemporaryToolExecutionAdaptersAreAbsent(t *testing.T) {
	root := repositoryRoot(t)
	for _, forbidden := range []string{
		"ProgressExecutor",
		"runnerProgress",
		"invocationAwareForegroundTool",
		"SetErrorSink",
		"AddCleanup",
		"httpExchangeProducer",
		"evidence.Emitter",
		"Command.Close",
	} {
		for _, tree := range []string{"agent", "core", "pkg", "tools", "cmd"} {
			err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "architecture_test.go") {
					return nil
				}
				content, readErr := os.ReadFile(path)
				if readErr != nil {
					return readErr
				}
				if bytes.Contains(content, []byte(forbidden)) {
					t.Errorf("temporary execution adapter %q remains in %s", forbidden, relative(root, path))
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestExtensionMigrationBoundaries(t *testing.T) {
	root := repositoryRoot(t)
	for _, rel := range []string{
		filepath.Join("pkg", "commands", "register.go"),
		filepath.Join("pkg", "commands", "read.go"),
		filepath.Join("pkg", "commands", "write.go"),
		filepath.Join("pkg", "commands", "glob.go"),
		filepath.Join("pkg", "commands", "list.go"),
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			t.Errorf("legacy production entry must stay removed: %s", filepath.ToSlash(rel))
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	assertNoImportPrefix(t, filepath.Join(root, "pkg", "toolset"), modulePath+"/core/capability")
	for _, tree := range []string{"agent", "core", "pkg", "tools", "cmd", "skills"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, forbidden := range []string{"platform" + "Tools", "capability.Register("} {
				if bytes.Contains(content, []byte(forbidden)) {
					t.Errorf("removed extension migration boundary %q returned in %s", forbidden, relative(root, path))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemovedRegistryAndObservationAbstractionsStayAbsent(t *testing.T) {
	root := repositoryRoot(t)
	for _, tree := range []string{"agent", "core", "pkg", "tools", "cmd", "skills"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for _, forbidden := range []string{
				"ExecutionOutcome",
				"EventEmitter",
				"ArtifactNormalizer",
				"ArtifactIngestor",
				"SubscribeEvents(",
				"wireWebApp",
				"SetArtifactIngestor",
				"ExtraNamespaces",
				"newEventBusEndpoint",
				"NewWithBus(",
				"NewTraffic(",
				"TrafficHandler",
				"SubscribeFlows(",
				"ErrConnectionCleanup",
				"request_body_ref",
				"response_body_ref",
				"\"tool_id\"",
				"toolset.New" + "Catalog(",
				"commands.New" + "Catalog(",
				modulePath + "/pkg/toolset/registry",
				modulePath + "/pkg/toolset/filetools",
				modulePath + "/pkg/toolset/workspacefiles",
				modulePath + "/pkg/exts/journal",
				modulePath + "/pkg/exts/fileaccess",
				modulePath + "/pkg/fileaudit",
				modulePath + "/pkg/shellaudit",
				modulePath + "/pkg/recording/httpcapture",
			} {
				if bytes.Contains(content, []byte(forbidden)) {
					t.Errorf("removed architecture boundary %q remains in %s", forbidden, relative(root, path))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	appDir := filepath.Join(root, "pkg", "app")
	err := filepath.WalkDir(appDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if bytes.Contains(content, []byte("EventBus")) {
			t.Errorf("App must not expose a second writable event bus: %s", relative(root, path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	toolNode := readRepositoryFile(t, root, filepath.Join("pkg", "toolnode", "node.go"))
	// A connection-owned protocol mux is not a resource lifecycle graph.
	// Reconnects may register shared protocols, but must not own extensions.
	for _, forbidden := range []string{"core/extension", "Extensions func"} {
		if strings.Contains(toolNode, forbidden) {
			t.Errorf("ToolNode must not own product extension lifecycles: found %q", forbidden)
		}
	}
}

func TestObservationProtocolsDoNotOwnLiveSubscriptionLifecycles(t *testing.T) {
	root := repositoryRoot(t)
	checks := map[string][]string{
		filepath.Join("web", "frontend", "cyber-ui", "packages", "aop", "proto", "aop", "file", "protocol.proto"): {
			"message WatchConfig", "message WatchState", "Configure configure = 21",
		},
		filepath.Join("web", "frontend", "cyber-ui", "packages", "aop", "proto", "aop", "traffic", "protocol.proto"): {
			"bool stream = 4",
		},
		filepath.Join("tools", "proxy", "traffic_handler.go"): {
			"SubscribeFlows(", "startStream(", "stopStreaming(",
		},
		filepath.Join("pkg", "node", "connection.go"): {
			"ConfigureFileObservation",
		},
	}
	for path, forbidden := range checks {
		source := readRepositoryFile(t, root, path)
		for _, value := range forbidden {
			if strings.Contains(source, value) {
				t.Errorf("live observation lifecycle %q remains in %s", value, filepath.ToSlash(path))
			}
		}
	}
}

func TestRunnerIsSingleTagFreeImplementation(t *testing.T) {
	root := repositoryRoot(t)
	for _, rel := range []string{filepath.Join("pkg", "runner"), filepath.Join("pkg", "exts", "agent"), filepath.Join("pkg", "console"), filepath.Join("cmd", "runner")} {
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
				// Terminal input has actual platform requirements; product
				// modes must still share one Console implementation.
				if rel == filepath.Join("pkg", "console") && (entry.Name() == "escape_unix.go" || entry.Name() == "escape_other.go") {
					continue
				}
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

	releaseWorkflow := readRepositoryFile(t, root, filepath.Join(".github", "workflows", "release-build.yml"))
	if count := strings.Count(releaseWorkflow, "main: ./cmd/runner"); count != 1 {
		t.Fatalf("CI release matrix must contain exactly one runner build, got %d", count)
	}
	if !strings.Contains(releaseWorkflow, "[[ \"$base\" == runner_* ]] && continue") {
		t.Error("release packaging must exclude runner archives")
	}
	if strings.Contains(releaseWorkflow, "runner_windows_amd64.zip") {
		t.Error("Windows release smoke must not require a runner archive")
	}

	goreleaser := readRepositoryFile(t, root, ".goreleaser.yml")
	if strings.Contains(goreleaser, "main: ./cmd/runner") || strings.Contains(goreleaser, "ids: [runner]") {
		t.Error("GoReleaser must not publish runner builds or archives")
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
	// Cross-cutting ownership and lifecycle tests intentionally do not have a
	// one-to-one production source file. They exercise a package boundary or a
	// resource lifetime assembled from several files.
	standalone := map[string]bool{
		"architecture_test.go":                     true,
		"session_architecture_test.go":             true,
		"aop/mux_lifecycle_test.go":                true,
		"cmd/aiscan/imports_default_test.go":       true,
		"cmd/aiscan/imports_full_test.go":          true,
		"cmd/aiscan/imports_record_full_test.go":   true,
		"core/extension/resource_test.go":          true,
		"core/extension/subscription_test.go":      true,
		"core/eventbus/lifecycle_test.go":          true,
		"agent/hooks/hooks_test.go":                true,
		"agent/tool_registry_test.go":              true,
		"pkg/commands/command_lifecycle_test.go":   true,
		"pkg/app/ownership_test.go":                true,
		"pkg/console/recorder_extension_test.go":   true,
		"pkg/exts/agent/command_ownership_test.go": true,
		"pkg/exts/agent/output_extension_test.go":  true,
		"pkg/exts/agent/ownership_test.go":         true,
		"pkg/host/example_test.go":                 true,
		"pkg/host/lifecycle_test.go":               true,
		"pkg/host/process_test.go":                 true,
		"cmd/runner/wire_test.go":                  true,
		"pkg/imageutil/encoding_test.go":           true,
		"pkg/web/service/config_lifecycle_test.go": true,
		"pkg/exts/agent/stdio_test.go":             true,
		"tools/files/lifecycle_test.go":            true,
		"tools/proxy/capture_lifecycle_test.go":    true,
		"tools/proxy/flow_store_lifecycle_test.go": true,
		"tools/proxy/hub_lifecycle_test.go":        true,
		"tools/record/register_test.go":            true,
	}
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
		// Repository scenarios span production files and have no matching source.
		if strings.HasPrefix(rel, "harness/") || standalone[filepath.ToSlash(rel)] {
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
	releaseWorkflow := readRepositoryFile(t, root, filepath.Join(".github", "workflows", "release-build.yml"))
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

	releaseWorkflow := readRepositoryFile(t, root, filepath.Join(".github", "workflows", "release-build.yml"))
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
		filepath.Join(".github", "workflows", "release-build.yml"),
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

func assertNoImportPrefix(t *testing.T, tree, forbidden string, allowed ...string) {
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
				permitted := false
				for _, prefix := range allowed {
					if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
						permitted = true
						break
					}
				}
				if permitted {
					continue
				}
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
	// Include new harness scenarios before staging, without scanning unrelated
	// untracked workspace artifacts elsewhere in the repository.
	cmd = exec.Command("git", "-C", root, "ls-files", "-z", "--others", "--exclude-standard", "--", "harness/", "architecture_test.go")
	added, err := cmd.Output()
	if err != nil {
		t.Fatalf("list new harness files: %v", err)
	}
	data = append(data, added...)
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

func TestIOAExtensionsAreIndependent(t *testing.T) {
	root := repositoryRoot(t)
	for _, tree := range []string{"core", "agent", "skills", "pkg/exts/agent", "pkg/profile", "pkg/console", "pkg/node", "pkg/probe", "pkg/web"} {
		assertNoImportPrefix(t, filepath.Join(root, tree), "github.com/chainreactors/ioa")
		assertNoImportPrefix(t, filepath.Join(root, tree), modulePath+"/tools/ioa")
	}
	assertNoImportPrefix(t, filepath.Join(root, "pkg/exts/ioa/client"), modulePath+"/pkg/exts/ioa/server")
	assertNoImportPrefix(t, filepath.Join(root, "pkg/exts/ioa/client"), modulePath+"/tools/ioa/server")
	assertNoImportPrefix(t, filepath.Join(root, "pkg/exts/ioa/client"), "github.com/chainreactors/ioa/server")
	assertNoImportPrefix(t, filepath.Join(root, "pkg/exts/ioa/server"), modulePath+"/pkg/exts/ioa/client")
	for _, tree := range []string{"pkg/console", "pkg/node", "pkg/runner", "pkg/probe", "pkg/web", "cmd/aiscan"} {
		assertNoImportPrefix(t, filepath.Join(root, tree), "github.com/chainreactors/ioa/client")
		assertNoImportPrefix(t, filepath.Join(root, tree), "github.com/chainreactors/ioa/server")
	}
}

func TestGenericHostsHaveNoTransitiveIOADependency(t *testing.T) {
	command := exec.Command("go", "list", "-deps", "./core/...", "./agent/...", "./pkg/profile", "./pkg/console/...", "./pkg/node", "./pkg/probe", "./pkg/web/...", "./skills")
	command.Dir = repositoryRoot(t)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dependency graph: %v\n%s", err, output)
	}
	for _, name := range strings.Fields(string(output)) {
		if strings.HasPrefix(name, "github.com/chainreactors/ioa") || strings.HasPrefix(name, modulePath+"/tools/ioa") || strings.HasPrefix(name, modulePath+"/pkg/exts/ioa") {
			t.Fatalf("generic hosts transitively depend on %s", name)
		}
	}
}
