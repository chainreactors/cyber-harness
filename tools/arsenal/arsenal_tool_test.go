package arsenal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	crtm "github.com/chainreactors/crtm/pkg"
	coretool "github.com/chainreactors/cyber/core/tool"
)

type versionSource struct{}

type waitingSource chan struct{}

func (s waitingSource) Resolve(ctx context.Context, _ crtm.Request) (crtm.Artifact, error) {
	close(s)
	<-ctx.Done()
	return crtm.Artifact{}, ctx.Err()
}

func TestInstallAndUpdateCancelDownload(t *testing.T) {
	for _, action := range []string{"install", "update"} {
		t.Run(action, func(t *testing.T) {
			source := make(waitingSource)
			manager, err := NewManager(t.TempDir(), crtm.ManagerOption{Sources: []crtm.Source{source}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := NewCommand(manager).Run(ctx, &coretool.Execution{Args: []string{action, "rg"}, Stdout: io.Discard})
				result <- err
			}()
			select {
			case <-source:
			case <-time.After(5 * time.Second):
				t.Fatal("download did not start")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("download ignored cancellation")
			}
		})
	}
}

func TestInstallRejectsMalformedArguments(t *testing.T) {
	cmd := newTestCmd(t)
	for _, args := range [][]string{
		{"rg", "--version"}, {"rg", "--version", ""}, {"rg", "--typo"},
		{"rg", "extra"}, {"rg", "--version", "1.0.0", "--version", "2.0.0"},
	} {
		for _, action := range []string{"install", "update"} {
			runErr(t, cmd, append([]string{action}, args...)...)
		}
	}
}

func (versionSource) Resolve(_ context.Context, req crtm.Request) (crtm.Artifact, error) {
	header := map[string]string{"windows": "MZxx", "linux": "\x7fELF", "darwin": "\xcf\xfa\xed\xfe"}[runtime.GOOS]
	return crtm.Artifact{Tool: req.Tool, Version: req.Version, Target: req.Target, Source: "fixture",
		Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(header + req.Version)), nil
		},
	}, nil
}

func TestInstallHonorsExplicitVersion(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir, crtm.ManagerOption{Sources: []crtm.Source{versionSource{}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.InstallVersion("gogo", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	cmd := &command{mgr: mgr}
	out := run(t, cmd, "install", "gogo", "--version", "2.0.0")
	if !strings.Contains(out, "v2.0.0") || mgr.InstalledVersion("gogo") != "2.0.0" {
		t.Fatal(out)
	}
	out = run(t, cmd, "install", "gogo", "--version", "v2.0.0")
	if !strings.Contains(out, "already installed") {
		t.Fatal(out)
	}
}

// run executes arsenal as a Command and returns stdout.
func run(t *testing.T, cmd *command, args ...string) string {
	t.Helper()
	var output bytes.Buffer
	_, err := cmd.Run(context.Background(), &coretool.Execution{Args: args, Stdout: &output, Stderr: &output})
	if err != nil {
		t.Fatalf("arsenal %s: %v", strings.Join(args, " "), err)
	}
	return output.String()
}

// runErr executes and expects an error.
func runErr(t *testing.T, cmd *command, args ...string) string {
	t.Helper()
	var output bytes.Buffer
	_, err := cmd.Run(context.Background(), &coretool.Execution{Args: args, Stdout: &output, Stderr: &output})
	if err == nil {
		t.Fatalf("arsenal %s: expected error, got output: %s", strings.Join(args, " "), output.String())
	}
	return err.Error()
}

func newTestCmd(t *testing.T) *command {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "bin")
	mgr, err := NewManager(dir, crtm.ManagerOption{})
	if err != nil {
		t.Fatal(err)
	}

	os.MkdirAll(binPath, 0o755)
	if path := os.Getenv("PATH"); !strings.Contains(path, binPath) {
		os.Setenv("PATH", binPath+string(os.PathListSeparator)+path)
	}

	return &command{mgr: mgr}
}

// --- Unit tests (offline) ---

func TestList(t *testing.T) {
	cmd := newTestCmd(t)
	out := run(t, cmd, "list")
	if !strings.Contains(out, "gogo") {
		t.Error("list should contain gogo")
	}
	if !strings.Contains(out, "nuclei") {
		t.Error("list should contain nuclei")
	}
	if !strings.Contains(out, "installed") {
		t.Error("list should show installed count")
	}
}

func TestSearchByName(t *testing.T) {
	cmd := newTestCmd(t)
	out := run(t, cmd, "search", "nuclei")
	if !strings.Contains(out, "nuclei") {
		t.Errorf("search should find nuclei, got: %s", out)
	}
}

func TestSearchNoArgs(t *testing.T) {
	cmd := newTestCmd(t)
	runErr(t, cmd, "search")
}

func TestInfoNotFound(t *testing.T) {
	cmd := newTestCmd(t)
	runErr(t, cmd, "info", "nonexistent_xyz")
}

func TestInstallNotFound(t *testing.T) {
	cmd := newTestCmd(t)
	runErr(t, cmd, "install", "nonexistent_xyz")
}

func TestInstallNoArgs(t *testing.T) {
	cmd := newTestCmd(t)
	runErr(t, cmd, "install")
}

func TestUpdateNoArgs(t *testing.T) {
	cmd := newTestCmd(t)
	runErr(t, cmd, "update")
}

func TestRemoveNotInstalled(t *testing.T) {
	cmd := newTestCmd(t)
	out := run(t, cmd, "remove", "gogo")
	if !strings.Contains(out, "not installed") {
		t.Errorf("expected 'not installed', got: %s", out)
	}
}

func TestRemoveNoArgs(t *testing.T) {
	cmd := newTestCmd(t)
	runErr(t, cmd, "remove")
}

func TestAddBadRepo(t *testing.T) {
	cmd := newTestCmd(t)
	runErr(t, cmd, "add", "noslash")
}

func TestAddAndFind(t *testing.T) {
	cmd := newTestCmd(t)

	out := run(t, cmd, "search", "ffuf")
	if strings.Contains(out, "ffuf/ffuf") {
		t.Fatal("ffuf should not exist before add")
	}

	out = run(t, cmd, "add", "ffuf/ffuf", "--pattern", "{name}_{version}_{os}_{arch}.tar.gz")
	if !strings.Contains(out, "Added ffuf") {
		t.Errorf("expected 'Added ffuf', got: %s", out)
	}

	out = run(t, cmd, "search", "ffuf")
	if !strings.Contains(out, "ffuf") {
		t.Errorf("ffuf should be findable after add, got: %s", out)
	}

	out = run(t, cmd, "add", "ffuf/ffuf")
	if !strings.Contains(out, "already registered") {
		t.Errorf("expected 'already registered', got: %s", out)
	}
}

func TestReleasesNoArgs(t *testing.T) {
	cmd := newTestCmd(t)
	runErr(t, cmd, "releases")
}

func TestUnknownAction(t *testing.T) {
	cmd := newTestCmd(t)
	runErr(t, cmd, "bad_action")
}

func TestUsageOnNoArgs(t *testing.T) {
	cmd := newTestCmd(t)
	out := run(t, cmd)
	if !strings.Contains(out, "Usage:") {
		t.Errorf("no args should show usage, got: %s", out)
	}
}

// --- E2E tests (real network) ---

func skipNetwork(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skip network test in short mode")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("e2e only on linux/amd64")
	}
}

func TestE2E_InstallCR(t *testing.T) {
	skipNetwork(t)
	cmd := newTestCmd(t)

	out := run(t, cmd, "install", "gogo")
	if !strings.Contains(out, "Installed gogo") {
		t.Errorf("expected install confirmation, got: %s", out)
	}

	// Idempotent.
	out = run(t, cmd, "install", "gogo")
	if !strings.Contains(out, "already installed") {
		t.Errorf("expected idempotent, got: %s", out)
	}

	// List shows version.
	out = run(t, cmd, "list")
	found := false
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "gogo") && strings.Contains(line, "*") {
			found = true
			t.Logf("gogo: %s", strings.TrimSpace(line))
		}
	}
	if !found {
		t.Error("gogo should show installed in list")
	}
}

func TestE2E_InstallPD(t *testing.T) {
	skipNetwork(t)
	cmd := newTestCmd(t)

	out := run(t, cmd, "install", "dnsx", "--version", "1.2.3")
	if !strings.Contains(out, "Installed dnsx") {
		t.Errorf("expected install confirmation, got: %s", out)
	}
	if !strings.Contains(out, "v1.2.3") {
		t.Errorf("expected version in output, got: %s", out)
	}
}

func TestE2E_InstallPDShowsHintDocs(t *testing.T) {
	skipNetwork(t)
	cmd := newTestCmd(t)

	out := run(t, cmd, "install", "nuclei")
	if !strings.Contains(out, "Docs:") {
		t.Errorf("install should show docs URL, got: %s", out)
	}
	if !strings.Contains(out, "Hint:") {
		t.Errorf("install should show hint, got: %s", out)
	}
}

func TestE2E_InfoShowsDocsHint(t *testing.T) {
	skipNetwork(t)
	cmd := newTestCmd(t)

	out := run(t, cmd, "info", "nuclei")
	if !strings.Contains(out, "Docs:") {
		t.Errorf("info should show docs, got: %s", out)
	}
	if !strings.Contains(out, "Hint:") {
		t.Errorf("info should show hint, got: %s", out)
	}
}

func TestE2E_AddAndInstall(t *testing.T) {
	skipNetwork(t)
	cmd := newTestCmd(t)

	runErr(t, cmd, "install", "ffuf") // not registered yet

	run(t, cmd, "add", "ffuf/ffuf", "--pattern", "{name}_{version}_{os}_{arch}.tar.gz")

	out := run(t, cmd, "install", "ffuf")
	if !strings.Contains(out, "Installed ffuf") {
		t.Errorf("expected install, got: %s", out)
	}

	info, _ := os.Stat(filepath.Join(cmd.mgr.BinPath(), "ffuf"))
	if info == nil || info.Size() < 1_000_000 {
		t.Error("ffuf binary should be >1MB")
	}
}

func TestE2E_UpdateTool(t *testing.T) {
	skipNetwork(t)
	cmd := newTestCmd(t)

	run(t, cmd, "install", "gogo")
	out := run(t, cmd, "update", "gogo")
	if !strings.Contains(out, "Updated gogo") {
		t.Errorf("expected update confirmation, got: %s", out)
	}
}

func TestE2E_RemoveTool(t *testing.T) {
	skipNetwork(t)
	cmd := newTestCmd(t)

	run(t, cmd, "install", "gogo")
	out := run(t, cmd, "remove", "gogo")
	if !strings.Contains(out, "Removed gogo") {
		t.Errorf("expected remove, got: %s", out)
	}

	out = run(t, cmd, "remove", "gogo")
	if !strings.Contains(out, "not installed") {
		t.Errorf("expected not installed, got: %s", out)
	}
}

func TestE2E_Releases(t *testing.T) {
	skipNetwork(t)
	cmd := newTestCmd(t)
	out := run(t, cmd, "releases", "gogo")
	if !strings.Contains(out, "version") {
		t.Errorf("releases should return version info, got: %s", out)
	}
}

func TestE2E_InstallThenExec(t *testing.T) {
	skipNetwork(t)
	cmd := newTestCmd(t)

	run(t, cmd, "install", "gogo")

	gogoPath, err := exec.LookPath("gogo")
	if err != nil {
		t.Fatalf("gogo not on PATH: %v", err)
	}
	t.Logf("gogo at: %s", gogoPath)

	out, _ := exec.Command("gogo", "-v").CombinedOutput()
	if len(out) == 0 {
		t.Error("gogo -v produced no output")
	}
}
