package telemetry

import (
	"fmt"
	"strings"
)

func StartupOK(component, detail string) string {
	return startupLine("", component, detail)
}

func StartupLine(status, component, detail string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		status = "info"
	}
	if status == "ok" {
		return StartupOK(component, detail)
	}
	return startupLine(status, component, detail)
}

func startupLine(status, component, detail string) string {
	component = strings.TrimSpace(component)
	detail = strings.TrimSpace(detail)
	if component == "" {
		component = "-"
	}
	if detail == "" {
		if status == "" {
			return component
		}
		return fmt.Sprintf("%-4s %s", status, component)
	}
	if status == "" {
		return fmt.Sprintf("%-12s %s", component, detail)
	}
	return fmt.Sprintf("%-4s %-12s %s", status, component, detail)
}
