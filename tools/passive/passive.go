// Package passive wraps uncover for cyberspace recon as the "passive" command.
package passive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"

	"github.com/projectdiscovery/uncover/sources"
)

const queryTimeout = 600 * time.Second

// Command dispatches passive recon to uncover by -s <source>.
type Command struct {
	engine  Engine
	logger  telemetry.Logger
	sources map[string]bool
}

// Engine is the passive command's minimal dependency, allowing callers
// to supply deterministic or alternate recon backends.
type Engine interface {
	Sources() []string
	Query(context.Context, string, string) ([]sources.Result, error)
}

// New creates a passive command. Engine may be nil (not configured).
func New(eng Engine) *Command {
	c := &Command{
		engine:  eng,
		logger:  telemetry.NopLogger(),
		sources: map[string]bool{},
	}
	if eng != nil {
		for _, s := range eng.Sources() {
			c.sources[s] = true
		}
	}
	return c
}

func (c *Command) WithLogger(l telemetry.Logger) *Command {
	if l != nil {
		c.logger = l
	}
	return c
}

func (c *Command) Name() string { return "passive" }

func (c *Command) Usage() string { return usage(c.sourceList()) }

// sourceExamples show the native query syntax of sources that have one worth
// illustrating. Usage prints only the examples of available sources.
var sourceExamples = map[string]string{
	"fofa":       `passive -s fofa 'domain="example.com"'`,
	"hunter":     `passive -s hunter 'domain.suffix="example.com"'`,
	"shodan":     `passive -s shodan 'org:"Example"'`,
	"shodan-idb": `passive -s shodan-idb '192.0.2.4'`,
}

func usage(avail []string) string {
	availStr := "decided by host configuration; run inside a configured session"
	reference := ""
	if len(avail) > 0 {
		availStr = strings.Join(avail, ", ")
		// The reference is mounted only together with a registered command.
		reference = "\n\nReference: " + ReferenceURI
	}
	var examples, notes strings.Builder
	for _, source := range avail {
		if example, ok := sourceExamples[source]; ok {
			examples.WriteString("\n  " + example)
		}
	}
	if examples.Len() == 0 {
		examples.WriteString("\n  passive -s <source> '<source-native query>'")
	}
	if slices.Contains(avail, "shodan-idb") {
		notes.WriteString("\n                Note: shodan-idb accepts ONLY ip/cidr queries.")
	}
	if slices.Contains(avail, "hunter") {
		notes.WriteString("\n                Note: hunter blocks overseas IPs; route it through the host proxy.")
	}
	return fmt.Sprintf(`passive - cyberspace passive recon (uncover)

Usage:%s

Options:
  -s <source>   Data source (required).
                Available now: %s%s
  -h            Show this help%s`, examples.String(), availStr, notes.String(), reference)
}

// QuickReference is the system-prompt entry of a registered command; it names
// the sources available now and the command's own reference document.
func (c *Command) QuickReference() string {
	return fmt.Sprintf("### passive — cyberspace passive recon (uncover)\n  passive -s <source> '<source-native query>'   Sources available now: %s\n  Reference: %s",
		strings.Join(c.sourceList(), ", "), ReferenceURI)
}

// ReferenceURI is the playbook the passive extension mounts with the command.
const ReferenceURI = "cyber://skills/passive/passive.md"

func (c *Command) Run(ctx context.Context, execution *coretool.Execution) (_ any, err error) {
	defer telemetry.RecoverAsError("passive", &err)
	args := execution.Args
	src, rest, help, err := splitSource(args)
	if err != nil {
		return nil, err
	}
	if help {
		fmt.Fprintln(execution.Stdout, c.Usage())
		return nil, nil
	}
	if c.sources[src] {
		result, err := c.runQuery(ctx, src, rest)
		if result != "" {
			fmt.Fprint(execution.Stdout, result)
		}
		return nil, err
	}
	return nil, fmt.Errorf("passive: unknown source %q (available: %v)", src, c.sourceList())
}

// --------------- query dispatch ----------------------------------------------

func (c *Command) runQuery(ctx context.Context, src string, args []string) (string, error) {
	if c.engine == nil {
		return "", fmt.Errorf("passive: no recon engine configured")
	}
	query, err := parseQueryArgs(args)
	if err != nil {
		return "", err
	}
	if src == "shodan-idb" && !looksLikeIPOrCIDR(query) {
		var others []string
		for _, source := range c.sourceList() {
			if source != src {
				others = append(others, source)
			}
		}
		if len(others) == 0 {
			return "", fmt.Errorf("passive: shodan-idb only accepts IP or CIDR queries (got %q); pass an IP or CIDR, or ask the host to configure a source for domain/keyword queries", query)
		}
		return "", fmt.Errorf("passive: shodan-idb only accepts IP or CIDR queries (got %q); other available sources, each with its own query language: %s", query, strings.Join(others, ", "))
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, queryTimeout)
		defer cancel()
	}
	results, queryErr := c.engine.Query(ctx, src, query)
	if queryErr != nil {
		queryErr = fmt.Errorf("passive: %w", queryErr)
		if len(results) == 0 {
			return "", queryErr
		}
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(rows(src, results)); err != nil {
		return buf.String(), fmt.Errorf("passive: encode: %w", err)
	}
	c.logger.Debugf("passive source=%s results=%d", src, len(results))
	return buf.String(), queryErr
}

// --------------- arg parsing -------------------------------------------------

//nolint:gosec // G602: the source value is bounds checked before advancing the argument index.
func splitSource(args []string) (source string, rest []string, help bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			help = true
			return
		case "-s", "--source":
			if i+1 >= len(args) {
				err = fmt.Errorf("passive: -s requires a value")
				return
			}
			source = args[i+1]
			i++
		default:
			rest = append(rest, args[i])
		}
	}
	if source == "" && !help {
		err = fmt.Errorf("passive: -s <source> is required (use -h for help)")
	}
	return
}

func parseQueryArgs(args []string) (query string, err error) {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			err = fmt.Errorf("passive: unknown flag %q for cyberspace source", a)
			return
		}
		if query != "" {
			err = fmt.Errorf("passive: multiple positional args; query must be a single quoted string")
			return
		}
		query = a
	}
	if query == "" {
		err = fmt.Errorf("passive: missing query; pass one source-native query string (see passive -h)")
	}
	return
}

// --------------- Python-compatible JSON shapes --------------------------------

type fofaRow struct {
	IP     string `json:"ip"`
	Port   string `json:"port"`
	URL    string `json:"url"`
	Domain string `json:"domain"`
	Title  string `json:"title"`
	ICP    string `json:"icp"`
}

type asset struct {
	IP     string `json:"ip"`
	Port   string `json:"port"`
	URL    string `json:"url"`
	Host   string `json:"host"`
	Source string `json:"source"`
}

func rows(src string, results []sources.Result) any {
	switch src {
	case "fofa":
		out := make([]fofaRow, 0, len(results))
		for _, r := range results {
			var raw Fofa
			_ = json.Unmarshal(r.Raw, &raw)
			out = append(out, fofaRow{
				IP: raw.IP, Port: raw.Port, URL: raw.Host,
				Domain: raw.Domain, Title: raw.Title, ICP: raw.ICP,
			})
		}
		return out
	case "hunter":
		out := make([]Hunter, 0, len(results))
		for _, r := range results {
			var raw Hunter
			_ = json.Unmarshal(r.Raw, &raw)
			out = append(out, raw)
		}
		return out
	default:
		out := make([]asset, 0, len(results))
		for _, r := range results {
			out = append(out, asset{
				IP:     r.IP,
				Port:   strconv.Itoa(r.Port),
				URL:    r.Url,
				Host:   r.Host,
				Source: r.Source,
			})
		}
		return out
	}
}

// --------------- helpers -----------------------------------------------------

func (c *Command) sourceList() []string {
	out := make([]string, 0, len(c.sources))
	for s := range c.sources {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func looksLikeIPOrCIDR(q string) bool {
	q = strings.TrimSpace(q)
	if q == "" {
		return false
	}
	if strings.Contains(q, "/") {
		prefix, err := netip.ParsePrefix(q)
		return err == nil && prefix.IsValid()
	}
	addr, err := netip.ParseAddr(q)
	return err == nil && addr.IsValid()
}
