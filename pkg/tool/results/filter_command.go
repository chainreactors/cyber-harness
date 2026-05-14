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
  filter_results --scanner <gogo|spray|zombie> --filter '{"key":"value",...}' [--operator <op>] [--limit N] [--data <json>]

If --data is omitted, reads from stdin. Run a scanner with -j flag first to get JSON output.

Options:
  --scanner   Which scanner produced the output (required)
  --filter    JSON object of key-value pairs for filtering (required)
  --operator  Match operator: contains, equals, not_contains, not_equals (default: contains)
  --limit     Maximum results to return (default: 50)
  --data      JSON-lines scanner output (alternative to stdin)`
}

func (c *FilterResultsCommand) Execute(_ context.Context, args []string) (string, error) {
	fs := flag.NewFlagSet("filter_results", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	scanner := fs.String("scanner", "", "")
	filterStr := fs.String("filter", "", "")
	operator := fs.String("operator", "contains", "")
	limit := fs.Int("limit", 50, "")
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
	if *data == "" {
		return "", fmt.Errorf("filter_results: --data is required")
	}

	var filter map[string]string
	if err := json.Unmarshal([]byte(*filterStr), &filter); err != nil {
		return "", fmt.Errorf("filter_results: invalid --filter JSON: %w", err)
	}

	lines := splitJSONLines(*data)
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
