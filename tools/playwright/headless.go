//go:build full

package playwright

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/pkg/headless"
	"github.com/chainreactors/neutron/protocols"
)

// execTemplate runs a nuclei-compatible headless template YAML file.
//
//	playwright template <file.yaml> <target-url> [--payload k=v ...]
func (c *Command) execTemplate(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright template: usage: playwright template <file.yaml> <target-url> [--payload k=v ...]")
	}

	templatePath := args[0]
	targetURL := args[1]

	// Parse --payload flags.
	payloads := make(map[string]interface{})
	for i := 2; i < len(args); i++ {
		if args[i] == "--payload" && i+1 < len(args) {
			i++
			parts := strings.SplitN(args[i], "=", 2)
			if len(parts) == 2 {
				payloads[parts[0]] = parts[1]
			}
		}
	}

	// Load and parse the template.
	tmpl, err := headless.LoadTemplate(templatePath)
	if err != nil {
		return "", fmt.Errorf("playwright template: %w", err)
	}

	// Ensure browser is running.
	browser, err := c.getOrLaunchBrowser()
	if err != nil {
		return "", fmt.Errorf("playwright template: %w", err)
	}

	// Create headless engine sharing our browser instance.
	engine := headless.NewEngine(headless.WithBrowser(browser))

	// Compile and execute.
	opts := &protocols.ExecuterOptions{Options: &protocols.Options{}}
	if err := tmpl.Compile(engine, opts); err != nil {
		return "", fmt.Errorf("playwright template: %w", err)
	}

	var results []map[string]interface{}
	result, err := tmpl.ExecuteWithCallback(targetURL, payloads, func(event *protocols.ResultEvent) {
		entry := map[string]interface{}{
			"template_id": event.TemplateID,
			"matched":     event.Matched,
			"type":        event.Type,
		}
		if event.MatcherName != "" {
			entry["matcher_name"] = event.MatcherName
		}
		if event.ExtractorName != "" {
			entry["extractor_name"] = event.ExtractorName
		}
		if len(event.ExtractedResults) > 0 {
			entry["extracted"] = event.ExtractedResults
		}
		results = append(results, entry)
	})

	if err != nil {
		return "", fmt.Errorf("playwright template: execution failed: %w", err)
	}

	// Format output.
	var out strings.Builder
	out.WriteString(fmt.Sprintf("Template: %s\n", tmpl.ID))
	out.WriteString(fmt.Sprintf("Target:   %s\n", targetURL))

	if result != nil && result.Matched {
		out.WriteString("Status:   MATCHED\n")
	} else {
		out.WriteString("Status:   NO MATCH\n")
	}

	if len(results) > 0 {
		out.WriteString("\nResults:\n")
		data, _ := json.MarshalIndent(results, "", "  ")
		out.Write(data)
		out.WriteByte('\n')
	}

	return out.String(), nil
}
