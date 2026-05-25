package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/chainreactors/aiscan/pkg/telemetry"
)

func agentPIDFilePath() string {
	return filepath.Join(os.TempDir(), "aiscan-agent.pid")
}

type agentPIDLock struct {
	path string
	file *os.File
	pid  int
}

func acquireAgentPIDFile(path string, logger telemetry.Logger) (*agentPIDLock, error) {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	pid := os.Getpid()
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open agent pidfile %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if existingPID, readErr := readAgentPIDFile(path); readErr == nil && existingPID > 0 {
			return nil, fmt.Errorf("another aiscan agent is already running (PID %d, pidfile %s); kill it first or remove the pidfile", existingPID, path)
		}
		return nil, fmt.Errorf("another aiscan agent is already running (pidfile %s is locked)", path)
	}
	locked := true
	cleanup := func() {
		if locked {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		}
		_ = f.Close()
	}

	if info, statErr := f.Stat(); statErr == nil && info.Size() > 0 {
		if existingPID, readErr := readAgentPIDFile(path); readErr == nil && existingPID > 0 && existingPID != pid {
			if processExists(existingPID) {
				cleanup()
				return nil, fmt.Errorf("another aiscan agent is already running (PID %d, pidfile %s); kill it first or remove the pidfile", existingPID, path)
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
	return &agentPIDLock{path: path, file: f, pid: pid}, nil
}

func (l *agentPIDLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = removeOwnedAgentPIDFile(l.path, l.pid)
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}

func removeOwnedAgentPIDFile(path string, pid int) error {
	existingPID, err := readAgentPIDFile(path)
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

func readAgentPIDFile(path string) (int, error) {
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

func processExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EPERM)
}
