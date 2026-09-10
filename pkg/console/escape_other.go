//go:build !unix

package console

import "time"

func readPendingTerminalBytes(_ time.Duration) string {
	return ""
}
