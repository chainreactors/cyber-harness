package arsenal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/commands"
	crtm "github.com/chainreactors/crtm/pkg"
)

// run executes arsenal as a Command and returns stdout.
func run(t *testing.T, cmd *ArsenalCommand, args ...string) string {
	t.Helper()
	commands.Output.Reset(nil)
	err := cmd.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("arsenal %s: %v", strings.Join(args, " "), err)
	}
	return commands.Output.Captured()
}

// runErr executes and expects an error.
func runErr(t *testing.T, cmd *ArsenalCommand, args ...string) string {
	t.Helper()
	commands.Output.Reset(nil)
	err := cmd.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("arsenal %s: expected error, got output: %s", strings.Join(args, " "), commands.Output.Captured())
	}
	return err.Error()
}

func newTestCmd(t *testing.T) *ArsenalCommand {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "bin")
	configPath := filepath.Join(dir, "config.yaml")

	mgr, err := crtm.NewManager(crtm.ManagerOption{
		BinPath:    binPath,
		ConfigPath: configPath,
	})
	if err != nil {
		t.Fatal(err)
	}

	os.MkdirAll(binPath, 0o755)
	if path := os.Getenv("PATH"); !strings.Contains(path, binPath) {
		os.Setenv("PATH", binPath+string(os.PathListSeparator)+path)
	}

	return &ArsenalCommand{mgr: mgr}
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
