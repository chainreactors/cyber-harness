//go:build !windows

package commands

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

func commandBridgeEndpoint(runtimeDir string) string {
	return filepath.Join(runtimeDir, "bridge.sock")
}

func listenCommandBridge(endpoint string) (net.Listener, error) {
	_ = os.Remove(endpoint)
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(endpoint, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}

func dialCommandBridge(ctx context.Context, endpoint string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", endpoint)
}

func createCommandBridgeAlias(executable, runtimeDir, name string) (string, error) {
	_ = executable
	path := filepath.Join(runtimeDir, name)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	content := "#!/bin/sh\n" + commandBridgeCommandEnv + "='" + name + "' exec \"$" + commandBridgeExecutableEnv + "\" \"$@\"\n"
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		return "", err
	}
	return path, nil
}

func commandBridgeProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func flushCommandBridgeProxyOutput() {
	_ = os.Stdout.Sync()
	_ = os.Stderr.Sync()
}
