//go:build linux || darwin

package task

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

type ptyHandle struct {
	ptmx *os.File
}

func startPTY(cmd *exec.Cmd) (*ptyHandle, error) {
	master, slave, err := openPTY()
	if err != nil {
		return nil, err
	}

	disableEcho(slave)

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setctty: true,
		Setsid:  true,
		Ctty:    3,
	}
	cmd.ExtraFiles = []*os.File{slave}
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave

	if err := cmd.Start(); err != nil {
		master.Close()
		slave.Close()
		return nil, err
	}
	slave.Close()
	return &ptyHandle{ptmx: master}, nil
}

func (p *ptyHandle) Read(buf []byte) (int, error)   { return p.ptmx.Read(buf) }
func (p *ptyHandle) Write(data []byte) (int, error)  { return p.ptmx.Write(data) }
func (p *ptyHandle) Close() error                    { return p.ptmx.Close() }

func openPTY() (master, slave *os.File, err error) {
	p, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}
	defer func() {
		if err != nil {
			p.Close()
		}
	}()

	name, err := ptsName(p)
	if err != nil {
		return nil, nil, err
	}
	if err := ptsGrant(p); err != nil {
		return nil, nil, err
	}
	if err := ptsUnlock(p); err != nil {
		return nil, nil, err
	}

	t, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open slave %s: %w", name, err)
	}
	return p, t, nil
}

func disableEcho(f *os.File) {
	fd := int(f.Fd())
	termios, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return
	}
	termios.Lflag &^= unix.ECHO | unix.ECHOE | unix.ECHOK | unix.ECHONL
	_ = unix.IoctlSetTermios(fd, ioctlSetTermios, termios)
}

// pumpOutput copies data from the PTY master into the OutputBuffer until EOF.
// Closes the returned channel when pumping is done.
func pumpOutput(r io.Reader, buf *OutputBuffer) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		tmp := make([]byte, 4096)
		for {
			n, err := r.Read(tmp)
			if n > 0 {
				_, _ = buf.Write(tmp[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	return done
}

func ptyIoctl(f *os.File, cmd, ptr uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), cmd, ptr)
	if errno != 0 {
		return errno
	}
	return nil
}
