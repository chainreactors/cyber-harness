//go:build darwin

package task

import (
	"bytes"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	ioctlGetTermios = unix.TIOCGETA
	ioctlSetTermios = unix.TIOCSETA

	_TIOCPTYGNAME  = 0x40807453
	_TIOCPTYGRANT  = 0x20007454
	_TIOCPTYUNLK   = 0x20007452
)

func ptsName(f *os.File) (string, error) {
	const parmLen = (_TIOCPTYGNAME >> 16) & 0x1fff
	out := make([]byte, parmLen)
	if err := ptyIoctl(f, _TIOCPTYGNAME, uintptr(unsafe.Pointer(&out[0]))); err != nil {
		return "", fmt.Errorf("TIOCPTYGNAME: %w", err)
	}
	i := bytes.IndexByte(out, 0)
	if i < 0 {
		i = len(out)
	}
	return string(out[:i]), nil
}

func ptsGrant(f *os.File) error {
	return ptyIoctl(f, _TIOCPTYGRANT, 0)
}

func ptsUnlock(f *os.File) error {
	return ptyIoctl(f, _TIOCPTYUNLK, 0)
}
