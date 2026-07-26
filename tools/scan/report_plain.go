package scan

import (
	"strings"
)

func formatPlainText(d *collector, fileLines []string) string {
	d.mu.Lock()
	defer d.mu.Unlock()

	var sb strings.Builder
	for _, line := range fileLines {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	sb.WriteString(formatScanSummaryLine(d, d.statsSnapshotLocked(), false))
	return sb.String()
}
