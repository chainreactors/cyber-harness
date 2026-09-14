package config

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveDataDir computes a path without creating directories or changing globals.
func ResolveDataDir(value string) string {
	if strings.TrimSpace(value) == "" {
		if executable, err := os.Executable(); err == nil {
			value = filepath.Join(filepath.Dir(executable), ".aiscan")
		} else {
			value = ".aiscan"
		}
	}
	if absolute, err := filepath.Abs(value); err == nil {
		return absolute
	}
	return value
}
