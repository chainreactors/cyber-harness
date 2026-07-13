package telemetry

import (
	"fmt"
	"strings"
)

func StartupOK(component, detail string) string {
	return StartupLine("ok", component, detail)
}

func StartupLine(status, component, detail string) string {
	status = strings.TrimSpace(status)
	component = strings.TrimSpace(component)
	detail = strings.TrimSpace(detail)
	if status == "" {
		status = "info"
	}
	if component == "" {
		component = "-"
	}
	line := fmt.Sprintf("%-4s %-12s", status, component)
	if detail != "" {
		line += " " + detail
	}
	return line
}
