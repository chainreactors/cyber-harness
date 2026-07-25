package toolargs

import (
	"strings"
)

func BoolFlagEnabled(args []string, long string) bool {
	for _, arg := range args {
		if arg == long {
			return true
		}
		if strings.HasPrefix(arg, long+"=") {
			return truthyFlagValue(strings.TrimPrefix(arg, long+"="))
		}
	}
	return false
}

func HasFlag(args []string, long string) bool {
	for _, arg := range args {
		if arg == long || strings.HasPrefix(arg, long+"=") {
			return true
		}
	}
	return false
}

func truthyFlagValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}
