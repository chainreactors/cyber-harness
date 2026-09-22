//go:build windows

package terminal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsBashRejectsWSLAndStoreStubs(t *testing.T) {
	for _, path := range []string{
		`C:\Windows\System32\bash.exe`,
		`C:\Windows\Sysnative\bash.exe`,
		`C:\Users\John\AppData\Local\Microsoft\WindowsApps\bash.exe`,
	} {
		if !windowsBashRejected(path) || nativeExecutable(path) {
			t.Fatalf("accepted %s", path)
		}
	}
	if windowsBashRejected(`C:\Users\John\scoop\apps\git\current\usr\bin\bash.exe`) {
		t.Fatal("rejected a Win32 bash path")
	}
}

func TestWindowsBashFoundWhenInstalled(t *testing.T) {
	selected := windowsBash()
	if selected != "" {
		if windowsBashRejected(selected) {
			t.Fatalf("selected rejected bash %s", selected)
		}
		return
	}
	home, _ := os.UserHomeDir()
	for _, candidate := range []string{
		filepath.Join(home, "scoop", "apps", "git", "current", "usr", "bin", "bash.exe"),
		`D:\SDK\msys2\usr\bin\bash.exe`,
	} {
		if nativeExecutable(candidate) {
			t.Fatalf("installed bash %s was not selected", candidate)
		}
	}
}
