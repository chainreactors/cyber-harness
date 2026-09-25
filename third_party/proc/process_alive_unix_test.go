//go:build unix

package proc

import (
	"os"
	"strings"
	"syscall"
)

// processAlive reports whether pid is still doing work. A zombie answers
// signal 0 successfully, and in a container whose init never reaps, an orphan
// stays a zombie forever -- so liveness has to look past the signal.
func processAlive(pid int) bool {
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		return false
	}
	status, err := os.ReadFile("/proc/" + itoa(pid) + "/stat")
	if err != nil {
		// Not Linux, or no procfs: the signal answer is all there is.
		return true
	}
	// The state letter follows the parenthesised command name.
	if index := strings.LastIndex(string(status), ") "); index >= 0 {
		return !strings.HasPrefix(string(status)[index+2:], "Z")
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
