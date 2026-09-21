package config

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveDataDir discovers storage without creating directories.
func ResolveDataDir(value string) string { return resolveDataDir(value, nil) }
func resolveDataDir(value string, context *Context) string {
	c := context.defaults()
	if strings.TrimSpace(value) == "" {
		value = filepath.Join(c.Home, ".cyber")
		for _, candidate := range []string{filepath.Join(c.Directory, ".cyber"), filepath.Join(filepath.Dir(c.Executable), ".cyber")} {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				value = candidate
				break
			}
		}
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(c.Directory, value)
	}
	return filepath.Clean(value)
}
