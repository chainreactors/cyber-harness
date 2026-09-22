//go:build windows

package terminal

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/chainreactors/utils/proc"
	"golang.org/x/sys/windows"
)

// shellProcess runs the shell on a pipe inside a kill-on-close job.
// ConPTY rewrites LF and trailing spaces. MSYS children also leave the
// process tree, so taskkill /T does not reach them; the job does.
func shellProcess(o proc.ProcOptions) proc.Attachment {
	return proc.AttachFunc(func(context.Context) (*proc.Attached, error) {
		if o.Binary == "" {
			return nil, errors.New("windows shell requires a binary")
		}
		cmd := exec.Command(o.Binary, o.Args...)
		cmd.Dir = o.Dir
		if len(o.Env) > 0 {
			cmd.Env = mergeShellEnv(os.Environ(), o.Env)
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP,
		}

		stdinR, stdinW, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		diagR, diagW, err := os.Pipe()
		if err != nil {
			_ = stdinR.Close()
			_ = stdinW.Close()
			return nil, err
		}
		cmd.Stdin = stdinR
		cmd.Stdout = diagW
		cmd.Stderr = diagW
		if err := cmd.Start(); err != nil {
			_ = stdinR.Close()
			_ = stdinW.Close()
			_ = diagR.Close()
			_ = diagW.Close()
			return nil, err
		}
		_ = stdinR.Close()
		_ = diagW.Close()

		job, _ := newKillJob()
		if job != 0 {
			if err := assignProcessJob(job, cmd.Process.Pid); err != nil {
				_ = windows.CloseHandle(job)
				job = 0
			}
		}
		if err := resumeProcess(cmd.Process.Pid); err != nil {
			if job != 0 {
				_ = windows.TerminateJobObject(job, 1)
				_ = windows.CloseHandle(job)
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			_ = stdinW.Close()
			_ = diagR.Close()
			return nil, err
		}

		pid := cmd.Process.Pid
		return &proc.Attached{
			Shape:  proc.ShapePipe,
			Wait:   func() proc.Result { return waitResult(cmd.Wait()) },
			Output: diagR,
			Input:  stdinW,
			Proc:   &proc.Proc{PID: pid},
			Signal: func(proc.Signal) error {
				if job != 0 {
					return windows.TerminateJobObject(job, 1)
				}
				return exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
			},
			Close: func() error {
				err := stdinW.Close()
				if job != 0 {
					_ = windows.CloseHandle(job)
				}
				return err
			},
		}, nil
	})
}

func mergeShellEnv(base, override []string) []string {
	keys := make(map[string]struct{}, len(override))
	for _, item := range override {
		if key, _, ok := strings.Cut(item, "="); ok {
			keys[key] = struct{}{}
		}
	}
	out := make([]string, 0, len(base)+len(override))
	for _, item := range base {
		if key, _, ok := strings.Cut(item, "="); ok {
			if _, replaced := keys[key]; replaced {
				continue
			}
		}
		out = append(out, item)
	}
	return append(out, override...)
}

func newKillJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func assignProcessJob(job windows.Handle, pid int) error {
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	return windows.AssignProcessToJobObject(job, handle)
}

func resumeProcess(pid int) error {
	handle, err := windows.OpenProcess(windows.PROCESS_SUSPEND_RESUME, false, uint32(pid))
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	status, _, callErr := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess").Call(uintptr(handle))
	if status != 0 {
		if callErr != nil && callErr != windows.ERROR_SUCCESS {
			return callErr
		}
		return syscall.Errno(status)
	}
	return nil
}

func waitResult(err error) proc.Result {
	if err == nil {
		return proc.Result{Exited: true}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return proc.Result{Err: err, Exited: true, ExitCode: exitErr.ExitCode()}
	}
	return proc.Result{Err: err}
}
