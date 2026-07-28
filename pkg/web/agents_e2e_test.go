//go:build e2e

package web

import "testing"

func TestE2ETerminalOpenAndType(t *testing.T) {
	runE2ETerminalOpenAndType(t)
}

func TestE2ETerminalResize(t *testing.T) {
	runE2ETerminalResize(t)
}
