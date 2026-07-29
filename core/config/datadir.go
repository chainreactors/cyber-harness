package config

import (
	"os"
	"path/filepath"
	"sync"
)

const dataDirName = ".aiscan"

var (
	dataDirMu       sync.Mutex
	resolvedDataDir string
	dataDirOnce     sync.Once
)

// SetDataDir sets the data directory explicitly (from -c/config).
// Must be called before any DataDir() call (typically during config resolution).
func SetDataDir(dir string) {
	dataDirMu.Lock()
	resolvedDataDir = dir
	dataDirMu.Unlock()
}

// DataDir returns the resolved .aiscan data directory.
// Priority is resolved centrally before this function is called:
// CLI > AISCAN_DATA_DIR > config > <binary_dir>/.aiscan.
func DataDir() string {
	dataDirOnce.Do(func() {
		dataDirMu.Lock()
		defer dataDirMu.Unlock()
		if resolvedDataDir == "" {
			if exe, err := os.Executable(); err == nil {
				resolvedDataDir = filepath.Join(filepath.Dir(exe), dataDirName)
			} else {
				resolvedDataDir = dataDirName
			}
		}
	})
	dataDirMu.Lock()
	defer dataDirMu.Unlock()
	return resolvedDataDir
}

// DataSubDir returns a subdirectory under DataDir, creating it if needed.
func DataSubDir(sub string) string {
	dir := filepath.Join(DataDir(), sub)
	_ = os.MkdirAll(dir, 0o755)
	return dir
}
