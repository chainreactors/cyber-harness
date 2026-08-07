package record

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	maxConcurrentEnv     = "AISCAN_RECORD_MAX_CONCURRENT"
	defaultMaxConcurrent = 4
	maxConcurrentLimit   = 16
)

type environmentLookup func(string) (string, bool)

func maxConcurrentFromEnvironment(lookup environmentLookup) (int, error) {
	if lookup == nil {
		return defaultMaxConcurrent, nil
	}
	raw, ok := lookup(maxConcurrentEnv)
	if !ok || strings.TrimSpace(raw) == "" {
		return defaultMaxConcurrent, nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer between 1 and %d", maxConcurrentEnv, maxConcurrentLimit)
	}
	if value < 1 || value > maxConcurrentLimit {
		return 0, fmt.Errorf("%s must be between 1 and %d", maxConcurrentEnv, maxConcurrentLimit)
	}
	return value, nil
}
