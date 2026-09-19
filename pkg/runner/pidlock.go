package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chainreactors/cyber/core/telemetry"
)

// agentPIDFilePath is the process-wide pidfile shared by scanner Agent runs.
func agentPIDFilePath() string {
	return filepath.Join(os.TempDir(), "cyber-agent.pid")
}

// pidLock is an exclusive advisory lock over the Agent pidfile. The lock lives
// on the open file handle so a crashed process cannot leave it held; the pid
// written into the file only reports who currently owns it.
type pidLock struct {
	path string
	file *os.File
	pid  int
}

func acquirePIDLock(path string, logger telemetry.Logger) (*pidLock, error) {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	pid := os.Getpid()
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open agent pidfile %s: %w", path, err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		if existingPID, readErr := readPIDFile(path); readErr == nil && existingPID > 0 {
			return nil, fmt.Errorf("another cyber agent is already running (PID %d, pidfile %s); kill it first or remove the pidfile", existingPID, path)
		}
		return nil, fmt.Errorf("another cyber agent is already running (pidfile %s is locked)", path)
	}
	locked := true
	cleanup := func() {
		if locked {
			_ = unlockFile(f)
		}
		_ = f.Close()
	}

	if info, statErr := f.Stat(); statErr == nil && info.Size() > 0 {
		if existingPID, readErr := readPIDFile(path); readErr == nil && existingPID > 0 && existingPID != pid {
			if processExists(existingPID) {
				cleanup()
				return nil, fmt.Errorf("another cyber agent is already running (PID %d, pidfile %s); kill it first or remove the pidfile", existingPID, path)
			}
			logger.Debugf("pidfile=%s stale_pid=%d action=reclaim", path, existingPID)
		} else if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			logger.Debugf("pidfile=%s action=rewrite reason=%q", path, readErr)
		}
	} else if statErr != nil {
		logger.Debugf("pidfile=%s action=rewrite reason=%q", path, statErr)
	}

	if err := f.Truncate(0); err != nil {
		cleanup()
		return nil, fmt.Errorf("truncate agent pidfile %s: %w", path, err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		cleanup()
		return nil, fmt.Errorf("seek agent pidfile %s: %w", path, err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", pid); err != nil {
		cleanup()
		return nil, fmt.Errorf("write agent pidfile %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return nil, fmt.Errorf("sync agent pidfile %s: %w", path, err)
	}
	locked = false
	return &pidLock{path: path, file: f, pid: pid}, nil
}

func (l *pidLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = removeOwnedPIDFile(l.path, l.pid)
	_ = unlockFile(l.file)
	_ = l.file.Close()
	l.file = nil
}

func removeOwnedPIDFile(path string, pid int) error {
	existingPID, err := readPIDFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if existingPID != pid {
		return nil
	}
	return os.Remove(path)
}

func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid %q", pidStr)
	}
	return pid, nil
}
