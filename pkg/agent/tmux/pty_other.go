//go:build !linux && !darwin

package tmux

import (
	"io"
	"os/exec"
)

// ptyHandle on unsupported platforms falls back to stdin/stdout pipes.
type ptyHandle struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func startPTY(cmd *exec.Cmd) (*ptyHandle, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &ptyHandle{stdin: stdin, stdout: stdout}, nil
}

func (p *ptyHandle) Read(buf []byte) (int, error)  { return p.stdout.Read(buf) }
func (p *ptyHandle) Write(data []byte) (int, error) { return p.stdin.Write(data) }

func (p *ptyHandle) Close() error {
	p.stdin.Close()
	return p.stdout.Close()
}

func pumpOutput(r io.Reader, buf *OutputBuffer) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		tmp := make([]byte, 4096)
		for {
			n, err := r.Read(tmp)
			if n > 0 {
				buf.Write(tmp[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	return done
}
