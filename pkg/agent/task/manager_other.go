//go:build !unix

package task

func signalProcessGroup(_ int, _ bool) error {
	return nil
}
