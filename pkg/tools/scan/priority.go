package scan

import (
	"fmt"
	"strings"
)

type priority string

const (
	priorityLow      priority = "low"
	priorityMedium   priority = "medium"
	priorityHigh     priority = "high"
	priorityCritical priority = "critical"
)

func parsePriority(value string) (priority, error) {
	switch priority(strings.ToLower(strings.TrimSpace(value))) {
	case "", priorityHigh:
		return priorityHigh, nil
	case priorityLow:
		return priorityLow, nil
	case priorityMedium:
		return priorityMedium, nil
	case priorityCritical:
		return priorityCritical, nil
	default:
		return "", fmt.Errorf("unknown priority %q, expected low, medium, high, or critical", value)
	}
}

func (p priority) atLeast(min priority) bool {
	return p.rank() >= min.rank()
}

func (p priority) rank() int {
	switch p {
	case priorityLow:
		return 1
	case priorityMedium:
		return 2
	case priorityHigh:
		return 3
	case priorityCritical:
		return 4
	default:
		return 0
	}
}
