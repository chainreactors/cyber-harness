//go:build !windows

package config

import "os"

func replaceFile(source, target string) error { return os.Rename(source, target) }
