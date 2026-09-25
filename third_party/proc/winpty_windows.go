//go:build windows

package proc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const winptySpawnFlagAutoShutdown = 1

var (
	winptyOnce sync.Once
	winptyErr  error

	modWinPTY                *syscall.LazyDLL
	procErrorCode            *syscall.LazyProc
	procErrorMsg             *syscall.LazyProc
	procErrorFree            *syscall.LazyProc
	procConfigNew            *syscall.LazyProc
	procConfigFree           *syscall.LazyProc
	procConfigSetInitialSize *syscall.LazyProc
	procOpen                 *syscall.LazyProc
	procConinName            *syscall.LazyProc
	procConoutName           *syscall.LazyProc
	procSpawnConfigNew       *syscall.LazyProc
	procSpawnConfigFree      *syscall.LazyProc
	procSpawn                *syscall.LazyProc
	procSetSize              *syscall.LazyProc
	procFree                 *syscall.LazyProc
)

type winPTY struct {
	handle  uintptr
	conin   *os.File
	conout  *os.File
	process uintptr
	closed  bool
}

func winPTYAvailable() bool {
	winptyOnce.Do(loadWinPTYLib)
	return winptyErr == nil
}

func loadWinPTYLib() {
	dir, err := extractWinPTYBin()
	if err != nil {
		winptyErr = err
		return
	}

	dllPath := filepath.Join(dir, "winpty.dll")
	if _, err := os.Stat(dllPath); err != nil {
		winptyErr = fmt.Errorf("winpty.dll not found: %w", err)
		return
	}
	agentPath := filepath.Join(dir, "winpty-agent.exe")
	if _, err := os.Stat(agentPath); err != nil {
		winptyErr = fmt.Errorf("winpty-agent.exe not found: %w", err)
		return
	}

	modWinPTY = syscall.NewLazyDLL(dllPath)
	procErrorCode = modWinPTY.NewProc("winpty_error_code")
	procErrorMsg = modWinPTY.NewProc("winpty_error_msg")
	procErrorFree = modWinPTY.NewProc("winpty_error_free")
	procConfigNew = modWinPTY.NewProc("winpty_config_new")
	procConfigFree = modWinPTY.NewProc("winpty_config_free")
	procConfigSetInitialSize = modWinPTY.NewProc("winpty_config_set_initial_size")
	procOpen = modWinPTY.NewProc("winpty_open")
	procConinName = modWinPTY.NewProc("winpty_conin_name")
	procConoutName = modWinPTY.NewProc("winpty_conout_name")
	procSpawnConfigNew = modWinPTY.NewProc("winpty_spawn_config_new")
	procSpawnConfigFree = modWinPTY.NewProc("winpty_spawn_config_free")
	procSpawn = modWinPTY.NewProc("winpty_spawn")
	procSetSize = modWinPTY.NewProc("winpty_set_size")
	procFree = modWinPTY.NewProc("winpty_free")

	if err := procErrorCode.Find(); err != nil {
		winptyErr = fmt.Errorf("winpty.dll load failed: %w", err)
		modWinPTY = nil
	}
}

func newWinPTY(cmd *exec.Cmd, cols, rows int) (*winPTY, error) {
	if !winPTYAvailable() {
		return nil, winptyErr
	}

	var errPtr uintptr
	defer procErrorFree.Call(errPtr)

	agentCfg, _, _ := procConfigNew.Call(0, uintptr(unsafe.Pointer(&errPtr)))
	if agentCfg == 0 {
		return nil, fmt.Errorf("winpty_config_new: %s", winptyErrMsg(errPtr))
	}
	procConfigSetInitialSize.Call(agentCfg, uintptr(cols), uintptr(rows))

	var openErr uintptr
	defer procErrorFree.Call(openErr)
	handle, _, _ := procOpen.Call(agentCfg, uintptr(unsafe.Pointer(&openErr)))
	procConfigFree.Call(agentCfg)
	if handle == 0 {
		return nil, fmt.Errorf("winpty_open: %s", winptyErrMsg(openErr))
	}

	coninName, _, _ := procConinName.Call(handle)
	coninHandle, err := openNamedPipe(coninName, syscall.GENERIC_WRITE)
	if err != nil {
		procFree.Call(handle)
		return nil, fmt.Errorf("open conin: %w", err)
	}

	conoutName, _, _ := procConoutName.Call(handle)
	conoutHandle, err := openNamedPipe(conoutName, syscall.GENERIC_READ)
	if err != nil {
		syscall.CloseHandle(coninHandle)
		procFree.Call(handle)
		return nil, fmt.Errorf("open conout: %w", err)
	}

	args := cmd.Args
	if len(args) == 0 {
		args = []string{cmd.Path}
	}
	cmdLine := strings.Join(args, " ")

	appName, _ := syscall.UTF16PtrFromString(cmd.Path)
	cmdLinePtr, _ := syscall.UTF16PtrFromString(cmdLine)
	cwd := cmd.Dir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	cwdPtr, _ := syscall.UTF16PtrFromString(cwd)

	var envPtr *uint16
	env := cmd.Env
	if len(env) > 0 {
		envPtr = createEnvBlock(env)
	}

	var spawnErr uintptr
	defer procErrorFree.Call(spawnErr)
	spawnCfg, _, _ := procSpawnConfigNew.Call(
		winptySpawnFlagAutoShutdown,
		uintptr(unsafe.Pointer(appName)),
		uintptr(unsafe.Pointer(cmdLinePtr)),
		uintptr(unsafe.Pointer(cwdPtr)),
		uintptr(unsafe.Pointer(envPtr)),
		uintptr(unsafe.Pointer(&spawnErr)),
	)
	if spawnCfg == 0 {
		syscall.CloseHandle(coninHandle)
		syscall.CloseHandle(conoutHandle)
		procFree.Call(handle)
		return nil, fmt.Errorf("winpty_spawn_config_new: %s", winptyErrMsg(spawnErr))
	}

	var processHandle uintptr
	var threadHandle uintptr
	var spawnErr2 uintptr
	defer procErrorFree.Call(spawnErr2)
	ret, _, _ := procSpawn.Call(
		handle, spawnCfg,
		uintptr(unsafe.Pointer(&processHandle)),
		uintptr(unsafe.Pointer(&threadHandle)),
		0,
		uintptr(unsafe.Pointer(&spawnErr2)),
	)
	procSpawnConfigFree.Call(spawnCfg)
	if ret == 0 {
		syscall.CloseHandle(coninHandle)
		syscall.CloseHandle(conoutHandle)
		procFree.Call(handle)
		return nil, fmt.Errorf("winpty_spawn: %s", winptyErrMsg(spawnErr2))
	}

	if threadHandle != 0 {
		syscall.CloseHandle(syscall.Handle(threadHandle))
	}

	return &winPTY{
		handle:  handle,
		conin:   os.NewFile(uintptr(coninHandle), "|0"),
		conout:  os.NewFile(uintptr(conoutHandle), "|1"),
		process: processHandle,
	}, nil
}

func (w *winPTY) Read(buf []byte) (int, error) {
	return w.conout.Read(buf)
}

func (w *winPTY) Write(data []byte) (int, error) {
	return w.conin.Write(data)
}

func (w *winPTY) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if modWinPTY != nil {
		procFree.Call(w.handle)
	}
	syscall.CloseHandle(syscall.Handle(w.process))
	w.conin.Close()
	w.conout.Close()
	return nil
}

func (w *winPTY) SetSize(cols, rows int) error {
	var errPtr uintptr
	defer procErrorFree.Call(errPtr)
	ret, _, _ := procSetSize.Call(w.handle, uintptr(cols), uintptr(rows), uintptr(unsafe.Pointer(&errPtr)))
	if ret == 0 {
		return fmt.Errorf("winpty_set_size: %s", winptyErrMsg(errPtr))
	}
	return nil
}

func (w *winPTY) PID() int {
	if w.process == 0 {
		return 0
	}
	pid, _ := getProcessID(w.process)
	return int(pid)
}

func (w *winPTY) Wait() error {
	if w.process == 0 {
		return nil
	}
	p, err := os.FindProcess(w.PID())
	if err != nil {
		return err
	}
	_, err = p.Wait()
	return err
}

func winptyErrMsg(ptr uintptr) string {
	if ptr == 0 {
		return "unknown error"
	}
	msg, _, _ := procErrorMsg.Call(ptr)
	if msg == 0 {
		return "unknown error"
	}
	return utf16PtrToString(msg)
}

// openNamedPipe opens a named pipe by its UTF-16 name pointer (returned as
// uintptr from winpty) using the raw CreateFileW syscall, avoiding an
// unsafe.Pointer round-trip that go vet flags.
func openNamedPipe(namePtr uintptr, access uint32) (syscall.Handle, error) {
	procCreateFileW := syscall.NewLazyDLL("kernel32.dll").NewProc("CreateFileW")
	r, _, e := procCreateFileW.Call(
		namePtr, uintptr(access), 0, 0,
		uintptr(syscall.OPEN_EXISTING), 0, 0,
	)
	h := syscall.Handle(r)
	if h == syscall.InvalidHandle {
		return h, e
	}
	return h, nil
}

var (
	procLstrlenW      = syscall.NewLazyDLL("kernel32.dll").NewProc("lstrlenW")
	procRtlMoveMemory = syscall.NewLazyDLL("kernel32.dll").NewProc("RtlMoveMemory")
)

// utf16PtrToString reads a NUL-terminated UTF-16 string at the address ptr
// (returned by a winpty C API). It avoids a direct uintptr→unsafe.Pointer
// cast by using kernel32 lstrlenW + RtlMoveMemory to copy into a Go slice.
func utf16PtrToString(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	n, _, _ := procLstrlenW.Call(ptr)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n)
	procRtlMoveMemory.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		ptr,
		n*2,
	)
	return syscall.UTF16ToString(buf)
}

func createEnvBlock(envv []string) *uint16 {
	if len(envv) == 0 {
		return &utf16.Encode([]rune("\x00\x00"))[0]
	}
	length := 0
	for _, s := range envv {
		length += len(s) + 1
	}
	length++
	b := make([]byte, length)
	i := 0
	for _, s := range envv {
		l := len(s)
		copy(b[i:i+l], s)
		b[i+l] = 0
		i = i + l + 1
	}
	b[i] = 0
	return &utf16.Encode([]rune(string(b)))[0]
}

func getProcessID(process uintptr) (uint32, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GetProcessId")
	r, _, e := syscall.SyscallN(proc.Addr(), process)
	if r == 0 {
		return 0, e
	}
	return uint32(r), nil
}
