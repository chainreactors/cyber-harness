//go:build windows

package terminal

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// System32 bash.exe is the WSL launcher, and WindowsApps bash.exe is a store
// stub. Neither can host the in-process command shims. Prefer a Win32 bash
// from Git, MSYS or scoop, and keep cmd.exe only when none is installed.
var windowsBash = sync.OnceValue(findWindowsBash)

func posixShellAvailable() bool { return windowsBash() != "" }

func commandShell(script string) (string, []string, bool) {
	if bash := windowsBash(); bash != "" {
		return bash, []string{"-c", script}, false
	}
	return "cmd", []string{"/c", script}, false
}

func pipeShell(script string) (string, []string) {
	binary, args, _ := commandShell(script)
	return binary, args
}

func prepareShellProcessEnv(env []string) []string {
	out := append([]string(nil), env...)
	bash := windowsBash()
	if bash == "" {
		return out
	}
	out = appendEnv(out, "MSYS_NO_PATHCONV", "1")
	out = appendEnv(out, "MSYS2_ARG_CONV_EXCL", "*")
	return prependPath(out, filepath.Dir(bash))
}

func appendEnv(env []string, key, value string) []string {
	for _, item := range env {
		if existing, _, ok := strings.Cut(item, "="); ok && existing == key {
			return env
		}
	}
	return append(env, key+"="+value)
}

func prependPath(env []string, dir string) []string {
	if dir == "" {
		return env
	}
	for i, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if !ok || !strings.EqualFold(key, "PATH") {
			continue
		}
		env[i] = key + "=" + dir + string(os.PathListSeparator) + value
		return env
	}
	return append(env, "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeShellCommandBashEnv(dir string, names []string) error {
	if windowsBash() == "" {
		return nil
	}
	var functions strings.Builder
	var scripts []string
	for _, name := range names {
		if bashIdentifier(name) {
			fmt.Fprintf(&functions, "%s() {\n  %s='%s' \"$%s\" \"$@\"\n}\nexport -f %s\n",
				name, shellCommandAdapterCommandEnv, name, shellCommandAdapterExecutableEnv, name)
			continue
		}
		scripts = append(scripts, name)
	}
	path := filepath.Join(dir, "commands.bash")
	if functions.Len() == 0 {
		_ = os.Remove(path)
	} else if err := os.WriteFile(path, []byte(functions.String()), 0o600); err != nil {
		return err
	}
	for _, name := range scripts {
		if err := writeBashShim(dir, name); err != nil {
			return err
		}
	}
	return nil
}

func bashIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func writeBashShim(dir, name string) error {
	path := filepath.Join(dir, name)
	content := "#!/bin/sh\n" + shellCommandAdapterCommandEnv + "='" + name + "' exec \"$" + shellCommandAdapterExecutableEnv + "\" \"$@\"\n"
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		return err
	}
	bash := windowsBash()
	cmd := exec.Command(bash, "-c", `chmod +x -- "$1"`, "chmod", filepath.ToSlash(path))
	cmd.Env = append(os.Environ(), "MSYS_NO_PATHCONV=1", "MSYS2_ARG_CONV_EXCL=*")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mark %s executable: %w (%s)", path, err, bytes.TrimSpace(out))
	}
	return nil
}

// msysPath is the form bash opens when MSYS_NO_PATHCONV disables argument
// conversion. C:/... is not a path to that bash; /c/... is.
func msysPath(path string) string {
	path = filepath.Clean(path)
	if len(path) >= 2 && path[1] == ':' {
		rest := filepath.ToSlash(path[2:])
		if !strings.HasPrefix(rest, "/") {
			rest = "/" + rest
		}
		return "/" + strings.ToLower(path[:1]) + rest
	}
	if strings.HasPrefix(path, `\\`) {
		return "/" + filepath.ToSlash(path)
	}
	return filepath.ToSlash(path)
}

func shellBashEnv(runtimeDir string) string {
	if windowsBash() == "" {
		return ""
	}
	path := filepath.Join(runtimeDir, "commands.bash")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return msysPath(path)
}

func findWindowsBash() string {
	var candidates []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || windowsBashRejected(dir) {
			continue
		}
		candidates = append(candidates, filepath.Join(dir, "bash.exe"))
	}
	if git, err := exec.LookPath("git"); err == nil && !windowsBashRejected(git) {
		root := filepath.Dir(filepath.Dir(git))
		candidates = append(candidates,
			filepath.Join(root, "usr", "bin", "bash.exe"),
			filepath.Join(root, "bin", "bash.exe"),
		)
	}
	for _, root := range []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("LocalAppData"),
	} {
		if root == "" {
			continue
		}
		base := root
		if strings.EqualFold(filepath.Base(root), "Local") || strings.HasSuffix(strings.ToLower(root), `\localappdata`) {
			base = filepath.Join(root, "Programs")
		}
		candidates = append(candidates,
			filepath.Join(base, "Git", "usr", "bin", "bash.exe"),
			filepath.Join(base, "Git", "bin", "bash.exe"),
		)
	}
	if home := os.Getenv("USERPROFILE"); home != "" {
		candidates = append(candidates,
			filepath.Join(home, "scoop", "apps", "git", "current", "usr", "bin", "bash.exe"),
			filepath.Join(home, "scoop", "apps", "git", "current", "bin", "bash.exe"),
		)
	}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		key := strings.ToLower(filepath.Clean(candidate))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if bash := nativeBash(candidate); bash != "" {
			return bash
		}
	}
	return ""
}

func nativeBash(path string) string {
	if !nativeExecutable(path) {
		return ""
	}
	dir := filepath.Dir(path)
	if strings.EqualFold(filepath.Base(dir), "bin") {
		usr := filepath.Join(filepath.Dir(dir), "usr", "bin", "bash.exe")
		if nativeExecutable(usr) {
			return usr
		}
	}
	return path
}

func nativeExecutable(path string) bool {
	if windowsBashRejected(path) {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func windowsBashRejected(path string) bool {
	cleaned := strings.ToLower(filepath.Clean(path))
	for _, fragment := range []string{`\windows\system32\`, `\windows\sysnative\`, `\windows\syswow64\`, `\windowsapps\`} {
		if strings.Contains(cleaned, fragment) {
			return true
		}
	}
	return false
}
