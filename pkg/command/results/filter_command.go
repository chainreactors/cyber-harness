package results

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
)

type FilterResultsCommand struct{}

func (c *FilterResultsCommand) Name() string { return "filter_results" }

func (c *FilterResultsCommand) Usage() string {
	return `filter_results - Filter JSON-lines scanner output by field criteria

Usage:
  filter_results --scanner <gogo|spray|zombie> --filter '{"key":"value",...}' [--file <path>|--data <json>] [--operator <op>] [--limit N]
  filter_results <gogo|spray|zombie> --filter <key=value[,key=value]> [--file <path>|--data <json>] [--operator <op>] [--limit N]

Run a scanner with -j flag first to get JSON-lines output. Prefer --file for large output.

Options:
  --scanner   Which scanner produced the output (required)
  --filter    JSON object or comma-separated key=value pairs for filtering (required)
  --operator  Match operator: contains, equals, not_contains, not_equals (default: contains)
  --limit     Maximum results to return (default: 50)
  --file      File containing JSON-lines scanner output
  --data      Inline JSON-lines scanner output`
}

func (c *FilterResultsCommand) Execute(_ context.Context, args []string) (string, error) {
	positionalScanner := leadingScannerArg(&args)
	fs := flag.NewFlagSet("filter_results", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	scanner := fs.String("scanner", positionalScanner, "")
	filterStr := fs.String("filter", "", "")
	operator := fs.String("operator", "contains", "")
	limit := fs.Int("limit", 50, "")
	file := fs.String("file", "", "")
	data := fs.String("data", "", "")
	if err := fs.Parse(args); err != nil {
		return "", fmt.Errorf("filter_results: %w\n\n%s", err, c.Usage())
	}
	if *scanner == "" {
		return "", fmt.Errorf("filter_results: --scanner is required\n\n%s", c.Usage())
	}
	if *filterStr == "" {
		return "", fmt.Errorf("filter_results: --filter is required\n\n%s", c.Usage())
	}
	if *data == "" {
		rest := strings.Join(fs.Args(), " ")
		if rest != "" {
			*data = rest
		}
	}
	resolvedData, err := readResultsData("filter_results", *data, *file)
	if err != nil {
		return "", err
	}

	filter, err := parseFilterArg(*filterStr)
	if err != nil {
		return "", err
	}

	lines := splitJSONLines(resolvedData)
	if len(lines) == 0 {
		return "No results to filter.", nil
	}

	switch *scanner {
	case "gogo":
		return filterGogoResults(lines, filter, *operator, *limit)
	case "spray":
		return filterSprayResults(lines, filter, *operator, *limit)
	case "zombie":
		return filterZombieResults(lines, filter, *operator, *limit)
	default:
		return "", fmt.Errorf("unsupported scanner: %s", *scanner)
	}
}

func parseFilterArg(raw string) (map[string]string, error) {
	var filter map[string]string
	if err := json.Unmarshal([]byte(raw), &filter); err == nil {
		return filter, nil
	}

	filter = make(map[string]string)
	for _, part := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("filter_results: invalid --filter %q: expected JSON object or key=value list", raw)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return nil, fmt.Errorf("filter_results: invalid --filter %q: empty key", raw)
		}
		filter[key] = value
	}
	if len(filter) == 0 {
		return nil, fmt.Errorf("filter_results: invalid --filter %q", raw)
	}
	return filter, nil
}
