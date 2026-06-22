package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const dataDirName = ".aiscan"

var (
	resolvedDataDir string
	dataDirOnce     sync.Once
)

// SetDataDir sets the data directory explicitly (from -c/config).
// Must be called before any DataDir() call (typically during config resolution).
func SetDataDir(dir string) {
	resolvedDataDir = dir
}

// DataDir returns the resolved .aiscan data directory.
// Priority: AISCAN_DATA_DIR env > config/CLI --data-dir > <binary_dir>/.aiscan
func DataDir() string {
	dataDirOnce.Do(func() {
		if v := strings.TrimSpace(os.Getenv("AISCAN_DATA_DIR")); v != "" {
			resolvedDataDir = v
		}
		if resolvedDataDir == "" {
			if exe, err := os.Executable(); err == nil {
				resolvedDataDir = filepath.Join(filepath.Dir(exe), dataDirName)
			} else {
				resolvedDataDir = dataDirName
			}
		}
	})
	return resolvedDataDir
}

// DataSubDir returns a subdirectory under DataDir, creating it if needed.
func DataSubDir(sub string) string {
	dir := filepath.Join(DataDir(), sub)
	_ = os.MkdirAll(dir, 0o755)
	return dir
}
