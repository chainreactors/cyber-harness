package config

import (
	"fmt"
	"strings"
)

// ResolveOutputFormat gives every host the same aliases and accepted values.
func ResolveOutputFormat(option *Option) error {
	format := option.OutputFormat
	if option.JSON {
		if option.Explicit["output-format"] && format != "" && !strings.EqualFold(strings.TrimSpace(format), "json") {
			return fmt.Errorf("conflicting output formats: -o %s and --json", format)
		}
		format = "json"
	}
	var err error
	option.OutputFormat, err = NormalizeOutputFormat(format)
	return err
}

func validateExecutionOptions(option *Option) error {
	if option.Command == RunModeInbox {
		if option.MaxActive < 0 {
			return fmt.Errorf("max-active must be positive (0 uses the default 4)")
		}
		if option.Resume != "" {
			return fmt.Errorf("inbox restores --state-dir; --resume is only supported by agent")
		}
	}
	if option.Timeout < 0 {
		return fmt.Errorf("--timeout must be nonnegative (0 disables it)")
	}
	return ResolveOutputFormat(option)
}

// NormalizeOutputFormat is shared by CLI validation and event rendering.
func NormalizeOutputFormat(format string) (string, error) {
	switch value := strings.ToLower(strings.TrimSpace(format)); value {
	case "", "text", "terminal":
		return "text", nil
	case "md", "markdown":
		return "markdown", nil
	case "stream-json", "jsonl":
		return "jsonl", nil
	case "json", "csv":
		return value, nil
	default:
		return "", fmt.Errorf("unsupported output format %q: use text, json, jsonl, markdown, or csv", format)
	}
}
