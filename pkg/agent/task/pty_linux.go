//go:build linux

package task

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	ioctlGetTermios = unix.TCGETS
	ioctlSetTermios = unix.TCSETS
)

func ptsName(f *os.File) (string, error) {
	var n uint32
	if err := ptyIoctl(f, syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); err != nil {
		return "", fmt.Errorf("TIOCGPTN: %w", err)
	}
	return "/dev/pts/" + strconv.Itoa(int(n)), nil
}

func ptsGrant(_ *os.File) error { return nil }

func ptsUnlock(f *os.File) error {
	var zero int
	return ptyIoctl(f, syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&zero)))
}
