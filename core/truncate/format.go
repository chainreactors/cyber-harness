package truncate

import (
	"fmt"
	"time"
)

// FormatSize returns a human-readable size string.
func FormatSize(bytes int) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%dB", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
	}
}

// FormatNumber renders an integer with thousands separators.
func FormatNumber(n int) string {
	if n < 0 {
		return "-" + FormatNumber(-n)
	}
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return FormatNumber(n/1000) + fmt.Sprintf(",%03d", n%1000)
}

// FormatDuration renders an elapsed duration at millisecond or second
// resolution, whichever the magnitude calls for.
func FormatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
