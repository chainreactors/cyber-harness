package toolchain

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	crtm "github.com/chainreactors/crtm/pkg"
)

func TestToolMetadataCurrent(t *testing.T) {
	spec, err := crtm.LoadBundleSpec("../../cmd/cyber-audit/bundle.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(spec, ToolSpec) {
		t.Fatal("tool metadata is stale; run make audit-arsenal-spec")
	}
	for _, os := range []string{"windows", "linux", "darwin"} {
		for _, arch := range []string{"amd64", "arm64"} {
			target := crtm.Target{GOOS: os, GOARCH: arch}
			want := []string{"rg", "ast-grep", "osv-scanner"}
			if arch == "amd64" && os != "darwin" {
				want = append(want, "capa", "floss")
				if os == "windows" {
					want = append(want, "radare2")
				}
			}
			got := spec.ToolsFor(target)
			if len(got) != len(want) {
				t.Fatalf("%s: %v, want %v", target, got, want)
			}
			catalog := crtm.NewCatalog(spec.CustomTools)
			for _, name := range want {
				entry, ok := catalog.Find(name)
				if !ok || entry.Version == "" || got[name] != entry.Version {
					t.Fatalf("%s missing from harness catalog", name)
				}
				if _, _, err := entry.AssetFor(entry.Version, os, arch); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestDoctorIsReadOnly(t *testing.T) {
	data := filepath.Join(t.TempDir(), "absent")
	manager, err := New(data)
	if err != nil {
		t.Fatal(err)
	}
	manager.lookup = func(string) (string, error) { return "", errors.New("missing") }
	manager.install = func(context.Context, string, string, func(context.Context, string) error) error {
		t.Fatal("doctor attempted an install")
		return nil
	}
	statuses := manager.Check(t.Context())
	if len(statuses) != len(Required) {
		t.Fatal(statuses)
	}
	for _, s := range statuses {
		if s.Error == "" {
			t.Fatal("missing tool marked healthy")
		}
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("doctor created data directory: %v", err)
	}
}
func TestEnsureInstallsOnlyMissingAndValidates(t *testing.T) {
	manager, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager.bundle = nil // This unit test injects its own installation behavior.
	versions := map[string]string{"path-rg": "15.2.0"}
	manager.lookup = func(name string) (string, error) {
		if name == "rg" {
			return "path-rg", nil
		}
		return "", errors.New("missing")
	}
	manager.probe = func(_ context.Context, s Spec, path string) (string, error) {
		v, ok := versions[path]
		if !ok {
			return "", errors.New("invalid")
		}
		return v, nil
	}
	installs := 0
	manager.install = func(ctx context.Context, name, version string, validate func(context.Context, string) error) error {
		installs++
		path := filepath.Join(manager.BinDir(), crtm.BinaryName(name))
		versions[path] = version
		if err := validate(ctx, path); err != nil {
			return err
		}
		if err := os.MkdirAll(manager.BinDir(), 0755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("fixture"), 0755)
	}
	for i := 0; i < 2; i++ {
		statuses, err := manager.Ensure(t.Context(), io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if len(statuses) != len(Required) {
			t.Fatal(statuses)
		}
	}
	if installs != len(Required)-1 {
		t.Fatalf("expected %d installs total, got %d", len(Required)-1, installs)
	}
}
func TestEnsureFailureAndCancellation(t *testing.T) {
	manager, _ := New(t.TempDir())
	manager.bundle = nil
	manager.lookup = func(string) (string, error) { return "", errors.New("missing") }
	manager.install = func(context.Context, string, string, func(context.Context, string) error) error {
		return errors.New("offline")
	}
	if _, err := manager.Ensure(t.Context(), io.Discard); err == nil {
		t.Fatal("missing required tool did not fail")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := manager.Ensure(ctx, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}
func TestVersionCompatibility(t *testing.T) {
	for _, test := range []struct {
		actual, minimum string
		want            bool
	}{{"15.2.0", "15.2.0", true}, {"15.3.0", "15.2.0", true}, {"16.0.0", "15.2.0", false}, {"0.45.4", "0.45.3", true}, {"0.46.0", "0.45.3", false}, {"2.5.9", "2.6.0", false}} {
		if got := compatible(test.actual, test.minimum); got != test.want {
			t.Errorf("%+v: got %v", test, got)
		}
	}
}
