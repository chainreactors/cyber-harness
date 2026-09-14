package record

import (
	"fmt"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/tools/record"
	"strconv"
	"strings"
)

const (
	maxConcurrentEnv     = "AISCAN_RECORD_MAX_CONCURRENT"
	defaultMaxConcurrent = record.DefaultMaxConcurrent
	maxConcurrentLimit   = record.MaxConcurrentLimit
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

const ConfigKey = "record"

type Options struct {
	MaxConcurrent int `long:"record-max-concurrent" config:"max_concurrent" json:"max_concurrent" description:"Maximum simultaneous recordings (1-16)"`
}

func Section() cfg.Section {
	return cfg.Section{Key: ConfigKey, Aliases: []string{ConfigKey}, New: func() any { return &Options{MaxConcurrent: defaultMaxConcurrent} }, Validate: func(value any) error {
		maximum := value.(*Options).MaxConcurrent
		if maximum < 1 || maximum > maxConcurrentLimit {
			return fmt.Errorf("max_concurrent must be between 1 and %d", maxConcurrentLimit)
		}
		return nil
	}, Environment: func(sources cfg.Sources) (map[string]any, map[string]any, error) {
		if _, explicit := sources.CLI["max_concurrent"]; explicit {
			return nil, nil, nil
		}
		raw, present := sources.LookupEnv(maxConcurrentEnv)
		if !present || strings.TrimSpace(raw) == "" {
			return nil, nil, nil
		}
		value, err := maxConcurrentFromEnvironment(sources.LookupEnv)
		return map[string]any{"max_concurrent": value}, nil, err
	}}
}
func ReadOptions(resolved *cfg.Resolved) (Options, error) {
	if resolved == nil {
		return Options{MaxConcurrent: defaultMaxConcurrent}, nil
	}
	value, err := cfg.Get[*Options](resolved, ConfigKey)
	if err != nil {
		return Options{}, err
	}
	return *value, nil
}
