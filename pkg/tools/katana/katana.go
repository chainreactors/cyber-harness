package katana

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const defaultTimeout = 120 * time.Second

// Command implements command.PseudoCommand for the katana web crawler.
// Unlike SDK-based tools (gogo, spray, neutron), katana runs as an
// external binary — this wrapper locates it at execution time and
// injects agent-friendly defaults.
type Command struct{}

func New() *Command { return &Command{} }

func (c *Command) Name() string { return "katana" }

func (c *Command) Usage() string {
	return `katana - deep web crawling with full parameter discovery
Usage: katana -u <url> [options]

Input:
  -u, -list          Target URL or file with URLs

Crawl:
  -d                 Crawl depth (default: 3)
  -jc                Enable JavaScript crawling
  -timeout           Timeout in seconds (default: 120)
  -ct                Crawl duration limit in seconds

Scope:
  -fs, -field-scope  Field scope (rdn, fqdn, dn)
  -cs, -crawl-scope  Crawl in-scope URLs regex
  -cos               Crawl out-of-scope URLs regex

Filter:
  -f, -field         Fields to output (url, path, fqdn, rdn, rurl, qurl, qpath, file, ufile, key, value, kv, dir, udir)
  -sf, -store-field  Fields to store (url, path, fqdn, rdn, rurl, qurl, qpath, file, ufile, key, value, kv, dir, udir)
  -em, -extension-match   Match extensions
  -ef, -extension-filter  Filter extensions

Output:
  -jsonl             JSON Lines output
  -silent            Silent mode
  -nc, -no-color     Disable colors

Examples:
  katana -u https://target.com -d 3 -jc
  katana -u https://target.com -d 2 -silent -jsonl
  katana -u https://target.com -f qurl
  katana -list urls.txt -d 2 -jc -timeout 60`
}

func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	katanaPath, err := exec.LookPath("katana")
	if err != nil {
		return "", fmt.Errorf("katana: binary not found in PATH — install with: go install github.com/projectdiscovery/katana/cmd/katana@latest")
	}

	args = withDefaultFlags(args)

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, katanaPath, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return stdout.String(), fmt.Errorf("katana: timed out")
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return stdout.String(), fmt.Errorf("katana: %s", errMsg)
		}
		return stdout.String(), fmt.Errorf("katana: %w", err)
	}

	return stdout.String(), nil
}

// withDefaultFlags injects -silent and -no-color when not already present,
// mirroring spray's withDefaultScannerFlags pattern.
func withDefaultFlags(args []string) []string {
	args = withDefaultBoolFlag(args, "-silent")
	args = withDefaultBoolFlag(args, "-no-color")
	return args
}

func withDefaultBoolFlag(args []string, flag string) []string {
	for _, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return args
		}
	}
	out := make([]string, 0, len(args)+1)
	out = append(out, args...)
	out = append(out, flag)
	return out
}
