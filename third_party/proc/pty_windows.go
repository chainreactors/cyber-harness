//go:build windows

package proc

import (
	"io"
	"os/exec"

	gopty "github.com/aymanbagabas/go-pty"
	"golang.org/x/sys/windows"
)

type ptyHandle struct {
	// go-pty (ConPTY) mode
	pty  gopty.Pty
	gcmd *gopty.Cmd

	// WinPTY mode
	wp *winPTY

	// pipe fallback mode
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cmd    *exec.Cmd
}

func startPTY(cmd *exec.Cmd) (*ptyHandle, error) {
	if conPTYAvailable() {
		if p, err := startGoPTY(cmd); err == nil {
			return p, nil
		}
	}
	if winPTYAvailable() {
		if p, err := startWinPTY(cmd); err == nil {
			return p, nil
		}
	}
	return startPipePTY(cmd)
}

func startGoPTY(cmd *exec.Cmd) (*ptyHandle, error) {
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
	return &ptyHandle{pty: p, gcmd: c}, nil
}

func startWinPTY(cmd *exec.Cmd) (*ptyHandle, error) {
	wp, err := newWinPTY(cmd, 120, 40)
	if err != nil {
		return nil, err
	}
	return &ptyHandle{wp: wp}, nil
}

func startPipePTY(cmd *exec.Cmd) (*ptyHandle, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	return &ptyHandle{stdin: stdin, stdout: stdout, cmd: cmd}, nil
}

func (p *ptyHandle) Read(buf []byte) (int, error) {
	switch {
	case p.pty != nil:
		return p.pty.Read(buf)
	case p.wp != nil:
		return p.wp.Read(buf)
	default:
		return p.stdout.Read(buf)
	}
}

func (p *ptyHandle) Write(data []byte) (int, error) {
	switch {
	case p.pty != nil:
		return p.pty.Write(data)
	case p.wp != nil:
		return p.wp.Write(data)
	default:
		return p.stdin.Write(data)
	}
}

func (p *ptyHandle) Close() error {
	switch {
	case p.pty != nil:
		return p.pty.Close()
	case p.wp != nil:
		return p.wp.Close()
	default:
		_ = p.stdin.Close()
		return p.stdout.Close()
	}
}

func (p *ptyHandle) PID() int {
	switch {
	case p.gcmd != nil && p.gcmd.Process != nil:
		return p.gcmd.Process.Pid
	case p.wp != nil:
		return p.wp.PID()
	case p.cmd != nil && p.cmd.Process != nil:
		return p.cmd.Process.Pid
	default:
		return 0
	}
}

func (p *ptyHandle) Wait() error {
	switch {
	case p.gcmd != nil:
		return p.gcmd.Wait()
	case p.wp != nil:
		return p.wp.Wait()
	case p.cmd != nil:
		return p.cmd.Wait()
	default:
		return nil
	}
}

func (p *ptyHandle) Resize(cols, rows int) error {
	switch {
	case p.pty != nil && cols > 0 && rows > 0:
		return p.pty.Resize(cols, rows)
	case p.wp != nil && cols > 0 && rows > 0:
		return p.wp.SetSize(cols, rows)
	default:
		return nil
	}
}

func conPTYAvailable() bool {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	for _, name := range []string{"CreatePseudoConsole", "ClosePseudoConsole", "ResizePseudoConsole"} {
		if err := kernel32.NewProc(name).Find(); err != nil {
			return false
		}
	}
	return true
}
