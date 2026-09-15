//go:build windows

package harness_test

import (
	"fmt"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureProductProcess(cmd *exec.Cmd) {
	// A private hidden console permits a real Ctrl+Break without signalling the
	// developer's terminal or unrelated processes. Product stdout stays in logs.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_CONSOLE | windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}

func interruptProduct(pid int) error {
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	free := kernel.NewProc("FreeConsole")
	free.Call()
	if ok, _, err := kernel.NewProc("AttachConsole").Call(uintptr(pid)); ok == 0 {
		return fmt.Errorf("attach product console: %w", err)
	}
	defer free.Call()
	// Address only the product's process group. Broadcasting to group zero also
	// interrupts the controller and races its console detach/Go race-detector exit.
	if ok, _, err := kernel.NewProc("GenerateConsoleCtrlEvent").Call(windows.CTRL_BREAK_EVENT, uintptr(pid)); ok == 0 {
		return fmt.Errorf("send Ctrl+Break: %w", err)
	}
	return nil
}
