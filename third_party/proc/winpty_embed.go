//go:build windows

package proc

import (
	"embed"
	"errors"
	"os"
	"path/filepath"
)

//go:embed winpty_bin/winpty.dll winpty_bin/winpty-agent.exe
var winptyFS embed.FS

func extractWinPTYBin() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(execPath)

	dllPath := filepath.Join(dir, "winpty.dll")
	agentPath := filepath.Join(dir, "winpty-agent.exe")

	if _, err := os.Stat(dllPath); errors.Is(err, os.ErrNotExist) {
		data, err := winptyFS.ReadFile("winpty_bin/winpty.dll")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(dllPath, data, 0o700); err != nil {
			return "", err
		}
	}

	if _, err := os.Stat(agentPath); errors.Is(err, os.ErrNotExist) {
		data, err := winptyFS.ReadFile("winpty_bin/winpty-agent.exe")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(agentPath, data, 0o700); err != nil {
			return "", err
		}
	}

	return dir, nil
}
