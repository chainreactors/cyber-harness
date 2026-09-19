package okf

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/chainreactors/cyber/pkg/commands"
)

// ReferenceURI is the command's virtual usage document.
const ReferenceURI = "cyber://skills/cyber/okf/runtime/okf.md"

func NewCommand() commands.Command {
	return commands.Command{
		Name:            "okf",
		Usage:           "okf <validate|test> <path> [--format text|json]",
		QuickReference:  "### okf - validate OKF knowledge bundles\n  okf validate <path>   Check official OKF 0.2 conformance\n  okf test <path>       Run strict production checks",
		DescriptionPath: ReferenceURI,
		Run:             runCommand,
	}
}

func runCommand(ctx context.Context, execution *commands.Execution) (any, error) {
	if execution == nil || len(execution.Args) < 2 {
		return nil, fmt.Errorf("usage: okf <validate|test> <path> [--format text|json]")
	}
	strict := false
	switch execution.Args[0] {
	case "validate":
	case "test":
		strict = true
	default:
		return nil, fmt.Errorf("unknown okf command %q", execution.Args[0])
	}
	format := "text"
	for i := 2; i < len(execution.Args); i++ {
		if execution.Args[i] == "--format" && i+1 < len(execution.Args) {
			format, i = execution.Args[i+1], i+1
		} else if strings.HasPrefix(execution.Args[i], "--format=") {
			format = strings.TrimPrefix(execution.Args[i], "--format=")
		} else {
			return nil, fmt.Errorf("unknown okf option %q", execution.Args[i])
		}
	}
	target := execution.Args[1]
	if !filepath.IsAbs(target) {
		target = filepath.Join(execution.Dir, target)
	}
	report, err := Validate(ctx, target, strict)
	if err != nil {
		return nil, err
	}
	switch format {
	case "json":
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return nil, err
		}
		fmt.Fprintln(execution.Stdout, string(encoded))
	case "text":
		mode := "validate"
		if strict {
			mode = "test"
		}
		fmt.Fprintf(execution.Stdout, "OKF %s %s: %d issue(s)\n", report.Version, mode, len(report.Issues))
		for _, issue := range report.Issues {
			fmt.Fprintf(execution.Stdout, "%s %s [%s] %s\n", strings.ToUpper(string(issue.Level)), issue.Path, issue.Rule, issue.Message)
		}
	default:
		return nil, fmt.Errorf("unsupported output format %q", format)
	}
	if !report.Valid() {
		return report, fmt.Errorf("OKF %s failed", execution.Args[0])
	}
	return report, nil
}
