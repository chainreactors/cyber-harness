//go:build !windows

package tui

import (
	"os"
	"os/signal"
	"syscall"
)

func (st *SplitTerminal) startResizeWatch() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		for {
			select {
			case <-st.stopCh:
				signal.Stop(sigCh)
				return
			case <-sigCh:
				st.handleResize()
			}
		}
	}()
}

func (st *SplitTerminal) stopResizeWatch() {
	// stopCh close in Teardown triggers the goroutine to exit and call signal.Stop.
}
