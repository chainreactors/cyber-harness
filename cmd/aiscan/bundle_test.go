package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	crtm "github.com/chainreactors/crtm/pkg"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	arsenalext "github.com/chainreactors/cyber/pkg/exts/arsenal"
	arsenaltool "github.com/chainreactors/cyber/tools/arsenal"
)

type offlineTransport struct{}

func TestArsenalMetadataCurrent(t *testing.T) {
	spec, err := crtm.LoadBundleSpec("bundle.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(spec, ToolSpec) {
		t.Fatal("tool metadata is stale; run make arsenal-spec")
	}
}

func (offlineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("network disabled for bundle test")
}

func TestArsenalInitializationOffline(t *testing.T) {
	old := http.DefaultTransport
	http.DefaultTransport = offlineTransport{}
	t.Cleanup(func() { http.DefaultTransport = old })
	bundle, err := EmbeddedBundle()
	if err != nil {
		t.Fatal(err)
	}
	manager, err := arsenaltool.NewManager(t.TempDir(), ToolSpec.ManagerOption(bundle))
	if err != nil {
		t.Fatal(err)
	}
	commands := coretool.NewCommandRegistry()
	set, err := extension.New(hosttest.Capabilities(), commands, arsenalext.New(manager))
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	var output bytes.Buffer
	_, err = commands.Execute(t.Context(), "arsenal", &coretool.Execution{Args: []string{"list"}, Stdout: &output})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "installed") {
		t.Fatal(output.String())
	}
	if bundle == nil {
		return
	}
	for _, entry := range manager.ListTools() {
		a, err := bundle.Resolve(t.Context(), crtm.Request{Tool: entry, Target: crtm.CurrentTarget()})
		if err != nil {
			continue
		}
		path := filepath.Join(manager.BinPath(), crtm.BinaryName(entry.Name))
		info, err := os.Stat(path)
		if err != nil || info.Size() != a.Size {
			t.Fatalf("%s was not prepared: %v", entry.Name, err)
		}
		if got := manager.InstalledVersion(entry.Name); got != a.Version {
			t.Fatalf("version %s, want %s", got, a.Version)
		}
		if entry.Name == "rg" {
			out, err := exec.Command(path, "--version").CombinedOutput()
			if err != nil || !strings.Contains(string(out), a.Version) {
				t.Fatalf("released ripgrep: %s, %v", out, err)
			}
		}
	}
}
