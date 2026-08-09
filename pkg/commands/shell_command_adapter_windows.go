//go:build windows

package commands

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func shellCommandAdapterEndpoint(runtimeDir string) string {
	return `\\.\pipe\aiscan-shell-commands-` + fmt.Sprintf("%d-%s", os.Getpid(), filepath.Base(runtimeDir))
}

func listenShellCommandAdapter(endpoint string) (net.Listener, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(endpoint, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + user.User.Sid.String() + ")",
		InputBufferSize:    shellCommandAdapterChunkSize,
		OutputBufferSize:   shellCommandAdapterChunkSize,
	})
}

func dialShellCommandAdapter(ctx context.Context, endpoint string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, endpoint)
}

func createShellCommandAdapterAlias(executable, runtimeDir, name string) (string, error) {
	_ = executable
	path := filepath.Join(runtimeDir, name+".cmd")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	content := "@echo off\r\n" +
		"set \"" + shellCommandAdapterCommandEnv + "=" + name + "\"\r\n" +
		"\"%" + shellCommandAdapterExecutableEnv + "%\" %*\r\n" +
		"exit /b %errorlevel%\r\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func shellCommandAdapterProcessAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return true
	}
	return exitCode == 259 // STILL_ACTIVE
}

func flushShellCommandAdapterProxyOutput() {
	_ = os.Stdout.Sync()
	_ = os.Stderr.Sync()
	// ConPTY can report the proxy process exit before consuming its final
	// console write. A short drain window prevents the last command in a chain
	// from losing output when cmd.exe exits immediately afterwards.
	time.Sleep(15 * time.Millisecond)
}
