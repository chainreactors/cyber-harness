//go:build !windows

package tool

import (
	"context"
	"os/exec"
	"syscall"
)

func runExternalCommand(ctx context.Context, cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(stopped)
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	})
	err := cmd.Wait()
	if !stop() {
		<-stopped
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
