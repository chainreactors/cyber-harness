//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func buildAiscan(t *testing.T) string {
	t.Helper()

	exe := filepath.Join(t.TempDir(), "aiscan-test.exe")
	args := []string{"build", "-tags", buildTags(), "-o", exe, "./cmd/aiscan"}
	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build aiscan: %v\n%s", err, out)
	}
	return exe
}

func repoRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}
