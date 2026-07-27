//go:build full && !windows

package main

import "os"

func replaceConfigFile(source, target string) error {
	return os.Rename(source, target)
}
