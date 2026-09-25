//go:build !windows

package proc

import (
	"os/exec"

	gopty "github.com/aymanbagabas/go-pty"
)

type ptyHandle struct {
	pty gopty.Pty
	cmd *gopty.Cmd
}

func startPTY(cmd *exec.Cmd) (*ptyHandle, error) {
	p, err := gopty.New()
	if err != nil {
		return nil, err
	}

	args := cmd.Args
	if len(args) == 0 {
		args = []string{cmd.Path}
	}
	c := p.Command(cmd.Path, args[1:]...)
	c.Args = args
	c.Env = cmd.Env
	c.Dir = cmd.Dir
	c.SysProcAttr = cmd.SysProcAttr
	c.Cancel = cmd.Cancel

	if err := c.Start(); err != nil {
		_ = p.Close()
		return nil, err
	}
	// The child owns the slave now. While the parent also holds it open, the
	// master never reaches EOF, so the output pump would block past the exit.
	if unixPTY, ok := p.(gopty.UnixPty); ok {
		_ = unixPTY.Slave().Close()
	}
	return &ptyHandle{pty: p, cmd: c}, nil
}

func (p *ptyHandle) Read(buf []byte) (int, error) {
	return p.pty.Read(buf)
}

func (p *ptyHandle) Write(data []byte) (int, error) {
	return p.pty.Write(data)
}

func (p *ptyHandle) Close() error {
	return p.pty.Close()
}

func (p *ptyHandle) PID() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *ptyHandle) Wait() error {
	if p.cmd == nil {
		return nil
	}
	return p.cmd.Wait()
}

func (p *ptyHandle) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return p.pty.Resize(cols, rows)
}
