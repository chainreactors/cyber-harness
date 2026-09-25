//go:build windows

package proc

import (
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

// setProcessGroup gives the child its own process group while leaving it
// attached to this process's console. That combination is what makes a console
// break addressable to the child alone, without this process detaching from or
// re-attaching to any console.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// signalProcessGroup delivers one rung of the ladder. Windows has no signal for
// the interrupt rung outside a console group, so a process that was not created
// with setProcessGroup reports ErrUnsupported and the ladder escalates.
func signalProcessGroup(pid int, sig Signal) error {
	if pid <= 0 {
		return nil
	}
	if sig == SigInterrupt {
		kernel := windows.NewLazySystemDLL("kernel32.dll")
		ok, _, err := kernel.NewProc("GenerateConsoleCtrlEvent").Call(windows.CTRL_BREAK_EVENT, uintptr(pid))
		if ok == 0 {
			return err
		}
		return nil
	}
	args := []string{"/PID", strconv.Itoa(pid), "/T"}
	if sig == SigKill {
		args = append(args, "/F")
	}
	return exec.Command("taskkill", args...).Run()
}

func exitSignal(*exec.ExitError) string { return "" }
