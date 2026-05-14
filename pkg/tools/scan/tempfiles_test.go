package scan

import (
	"os"
	"path/filepath"
	"testing"
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

	filename := filepath.Join(dir, gogoTempLogFile)
	if err := os.WriteFile(filename, []byte("temp"), 0o600); err != nil {
		t.Fatal(err)
	}

	cleanupGogoTempFiles()

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

	cleanupGogoTempFiles()
}
