package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
)

func TestCleanupGogoTempFilesRemovesSockLock(t *testing.T) {
	dir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	}()

	filename := filepath.Join(dir, engine.GogoTempLogFile)
	if err := os.WriteFile(filename, []byte("temp"), 0o600); err != nil {
		t.Fatal(err)
	}

	engine.CleanupGogoTempFiles()

	if _, err := os.Stat(filename); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, stat error = %v", filename, err)
	}
}

func TestCleanupGogoTempFilesIgnoresMissingFile(t *testing.T) {
	dir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	}()

	engine.CleanupGogoTempFiles()
}
