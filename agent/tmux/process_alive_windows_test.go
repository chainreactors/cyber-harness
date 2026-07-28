//go:build windows

package tmux

func processAlive(int) bool {
	return true
}
