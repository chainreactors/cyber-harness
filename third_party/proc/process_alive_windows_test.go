//go:build windows

package proc

func processAlive(int) bool { return false }
