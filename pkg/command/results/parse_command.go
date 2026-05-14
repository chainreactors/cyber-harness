package results

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
)

type ParseResultsCommand struct{}

func (c *ParseResultsCommand) Name() string { return "parse_results" }

func (c *ParseResultsCommand) Usage() string {
	return `parse_results - Parse JSON-lines scanner output into structured analysis

Usage:
  parse_results --scanner <gogo|spray|zombie> --analysis <summary|targets|stats|all> [--data <json>]

If --data is omitted, reads from stdin. Run a scanner with -j flag first to get JSON output.

Options:
  --scanner   Which scanner produced the output (required)
  --analysis  What analysis to return: summary, targets, stats, all (default: all)
  --data      JSON-lines scanner output (alternative to stdin)`
}

func (c *ParseResultsCommand) Execute(_ context.Context, args []string) (string, error) {
	fs := flag.NewFlagSet("parse_results", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	scanner := fs.String("scanner", "", "")
	analysis := fs.String("analysis", "all", "")
	data := fs.String("data", "", "")
	if err := fs.Parse(args); err != nil {
		return "", fmt.Errorf("parse_results: %w\n\n%s", err, c.Usage())
	}
	if *scanner == "" {
		return "", fmt.Errorf("parse_results: --scanner is required\n\n%s", c.Usage())
	}
	if *data == "" {
		rest := strings.Join(fs.Args(), " ")
		if rest != "" {
			*data = rest
		}
	}
	if *data == "" {
		return "", fmt.Errorf("parse_results: --data is required")
	}

	lines := splitJSONLines(*data)
	if len(lines) == 0 {
		return "No results to parse.", nil
	}

	switch *scanner {
	case "gogo":
		return parseGogoResults(lines, *analysis)
	case "spray":
		return parseSprayResults(lines, *analysis)
	case "zombie":
		return parseZombieResults(lines, *analysis)
	default:
		return "", fmt.Errorf("unsupported scanner: %s", *scanner)
	}
}
