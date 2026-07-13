//go:build windows

package tui

import "time"

// Windows has no SIGWINCH. Poll terminal size periodically instead.
func (st *SplitTerminal) startResizeWatch() {
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-st.stopCh:
				return
			case <-ticker.C:
				st.handleResize()
			}
		}
	}()
}

func (st *SplitTerminal) stopResizeWatch() {
	// stopCh close in Teardown triggers the goroutine to exit.
}
