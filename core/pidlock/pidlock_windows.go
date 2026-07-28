//go:build windows

package pidlock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const lockOffset = 0xfffffff0

func lockFile(f *os.File) error {
	overlapped := windows.Overlapped{Offset: lockOffset}
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&overlapped,
	)
}

func unlockFile(f *os.File) error {
	overlapped := windows.Overlapped{Offset: lockOffset}
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped)
}

func ProcessExists(pid int) bool {
	if pid <= 0 || uint64(pid) > uint64(^uint32(0)) {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // pid is bounded to uint32 above
	if err == nil {
		_ = windows.CloseHandle(handle)
		return true
	}
	return errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
