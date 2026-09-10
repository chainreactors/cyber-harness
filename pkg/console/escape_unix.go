//go:build unix

package console

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func readPendingTerminalBytes(timeout time.Duration) string {
	file := os.Stdin
	fd := int32(file.Fd()) //nolint:gosec // stdin fd is always small
	timeoutMS := int(timeout / time.Millisecond)
	if timeoutMS <= 0 && timeout > 0 {
		timeoutMS = 1
	}

	fds := []unix.PollFd{{
		Fd:     fd,
		Events: unix.POLLIN,
	}}
	n, err := unix.Poll(fds, timeoutMS)
	if err != nil || n <= 0 {
		return ""
	}
	if fds[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) == 0 {
		return ""
	}

	buf := make([]byte, 64)
	read, err := file.Read(buf)
	if err != nil || read <= 0 {
		return ""
	}
	return string(buf[:read])
}
