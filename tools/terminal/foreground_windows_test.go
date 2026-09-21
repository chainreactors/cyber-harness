//go:build windows

package terminal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestForegroundCancelDoesNotRunRemainingCommands(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "timeout"}[timeout], func(t *testing.T) {
			dir := t.TempDir()
			bash := NewBashTool(dir, 10, nil)
			defer bash.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			budget := 10 * time.Second
			if timeout {
				budget = time.Second
			}
			done := make(chan error, 1)
			go func() {
				_, err := bash.RunForeground(ctx, "echo started > started.txt & ping -n 30 127.0.0.1 > nul & echo leaked > leaked.txt", BashExecOptions{Timeout: budget, TimeoutSet: true})
				done <- err
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(dir, "started.txt")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("shell did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !timeout {
				cancel()
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("foreground execution did not drain")
			}
			if _, err := os.Stat(filepath.Join(dir, "leaked.txt")); !os.IsNotExist(err) {
				t.Fatalf("canceled command continued: %v", err)
			}
		})
	}
}
