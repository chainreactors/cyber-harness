package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/chainreactors/aiscan/pkg/telemetry"
)

func TestAcquireAgentPIDFileRejectsHeldLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.pid")
	first, err := acquireAgentPIDFile(path, telemetry.NopLogger())
	if err != nil {
		t.Fatalf("first acquireAgentPIDFile() error = %v", err)
	}
	defer first.Release()

	_, err = acquireAgentPIDFile(path, telemetry.NopLogger())
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("acquireAgentPIDFile() error = %v, want already running", err)
	}
}

func TestAcquireAgentPIDFileReclaimsInvalidPIDFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.pid")
	if err := os.WriteFile(path, []byte("not-a-pid\n"), 0644); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}

	lock, err := acquireAgentPIDFile(path, telemetry.NopLogger())
	if err != nil {
		t.Fatalf("acquireAgentPIDFile() error = %v", err)
	}
	defer lock.Release()

	got, err := readAgentPIDFile(path)
	if err != nil {
		t.Fatalf("readAgentPIDFile() error = %v", err)
	}
	if got != os.Getpid() {
		t.Fatalf("pidfile pid = %d, want %d", got, os.Getpid())
	}
}

func TestAcquireAgentPIDFileIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.pid")
	const workers = 8
	type result struct {
		lock *agentPIDLock
		err  error
	}

	var wg sync.WaitGroup
	results := make(chan result, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lock, err := acquireAgentPIDFile(path, telemetry.NopLogger())
			results <- result{lock: lock, err: err}
		}()
	}
	wg.Wait()
	close(results)

	successes := 0
	for result := range results {
		if result.err == nil {
			successes++
			result.lock.Release()
			continue
		}
		if !strings.Contains(result.err.Error(), "already running") {
			t.Fatalf("unexpected acquire error: %v", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful acquisitions = %d, want 1", successes)
	}
}

func TestReleaseAgentPIDFileOnlyRemovesOwnedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.pid")
	lock, err := acquireAgentPIDFile(path, telemetry.NopLogger())
	if err != nil {
		t.Fatalf("acquireAgentPIDFile() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("1\n"), 0644); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}

	lock.Release()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("pidfile should remain when owned by another pid: %v", err)
	}
}
