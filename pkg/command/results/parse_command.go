package results

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

type ParseResultsCommand struct{}

func (c *ParseResultsCommand) Name() string { return "parse_results" }

func (c *ParseResultsCommand) Usage() string {
	return `parse_results - Parse JSON-lines scanner output into structured analysis

Usage:
  parse_results --scanner <gogo|spray|zombie> [--file <path>|--data <json>] [--analysis <summary|targets|stats|all>]
  parse_results <gogo|spray|zombie> [--file <path>|--data <json>] [--analysis <summary|targets|stats|all>]

Run a scanner with -j flag first to get JSON-lines output. Prefer --file for large output.

Options:
  --scanner   Which scanner produced the output (required)
  --file      File containing JSON-lines scanner output
  --analysis  What analysis to return: summary, targets, stats, all (default: all)
  --data      Inline JSON-lines scanner output`
}

func (c *ParseResultsCommand) Execute(_ context.Context, args []string) (string, error) {
	positionalScanner := leadingScannerArg(&args)
	fs := flag.NewFlagSet("parse_results", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	scanner := fs.String("scanner", positionalScanner, "")
	analysis := fs.String("analysis", "all", "")
	file := fs.String("file", "", "")
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
	resolvedData, err := readResultsData("parse_results", *data, *file)
	if err != nil {
		return "", err
	}

	lines := splitJSONLines(resolvedData)
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

func leadingScannerArg(args *[]string) string {
	if len(*args) == 0 || strings.HasPrefix((*args)[0], "-") {
		return ""
	}
	scanner := (*args)[0]
	*args = (*args)[1:]
	return scanner
}

func readResultsData(commandName, data, file string) (string, error) {
	if data != "" {
		return data, nil
	}
	if file == "" {
		return "", fmt.Errorf("%s: --data or --file is required", commandName)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("%s: read --file: %w", commandName, err)
	}
	return string(b), nil
}
