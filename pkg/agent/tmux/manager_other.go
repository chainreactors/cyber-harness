//go:build !unix

package tmux

func signalProcessGroup(_ int, _ bool) error {
	return nil
}
