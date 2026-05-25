//go:build full

// Package passive wraps uncover for cyberspace recon as the "passive" command.
package passive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/projectdiscovery/uncover/sources"
)

const queryTimeout = 600 * time.Second

// Command dispatches passive recon to uncover by -s <source>.
type Command struct {
	engine  *engine.UncoverEngine
	logger  telemetry.Logger
	sources map[string]bool
}

// New creates a passive command. Engine may be nil (not configured).
func New(eng *engine.UncoverEngine) *Command {
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

func (c *Command) Usage() string {
	return `passive - cyberspace passive recon (uncover)

Usage:
  passive -s fofa 'domain="example.com"'
  passive -s hunter 'domain.suffix="example.com"'
  passive -s shodan 'org:"Example"'

Options:
  -s <source>   Data source (required).
                Cyberspace: fofa, hunter, shodan, shodan-idb, censys,
                            quake, zoomeye, netlas, criminalip, publicwww,
                            hunterhow, binaryedge, onyphe, driftnet, greynoise
  -h            Show this help`
}

func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	src, rest, help, err := splitSource(args)
	if err != nil {
		return "", err
	}
	if help {
		return c.Usage(), nil
	}
	if c.sources[src] {
		return c.runQuery(ctx, src, rest)
	}
	return "", fmt.Errorf("passive: unknown source %q (available: %v)", src, c.sourceList())
}

// --------------- query dispatch ----------------------------------------------

func (c *Command) runQuery(ctx context.Context, src string, args []string) (string, error) {
	if c.engine == nil {
		return "", fmt.Errorf("passive: uncover engine not initialized — set recon credentials")
	}
	query, err := parseQueryArgs(args)
	if err != nil {
		return "", err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, queryTimeout)
		defer cancel()
	}
	results, err := c.engine.QueryRaw(ctx, src, query)
	if err != nil {
		return "", fmt.Errorf("passive: %w", err)
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(uncoverPython(src, results)); err != nil {
		return buf.String(), fmt.Errorf("passive: encode: %w", err)
	}
	c.logger.Debugf("passive source=%s results=%d", src, len(results))
	return buf.String(), nil
}

// --------------- arg parsing -------------------------------------------------

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
		err = fmt.Errorf("passive: missing query (e.g. passive -s fofa 'domain=\"example.com\"')")
	}
	return
}

// --------------- Python-compatible JSON shapes --------------------------------

type pyFofa struct {
	IP     string `json:"ip"`
	Port   string `json:"port"`
	URL    string `json:"url"`
	Domain string `json:"domain"`
	Title  string `json:"title"`
	ICP    string `json:"icp"`
}

type pyHunter struct {
	IP      string `json:"ip"`
	Port    string `json:"port"`
	URL     string `json:"url"`
	Domain  string `json:"domain"`
	Status  string `json:"status"`
	Company string `json:"company"`
	Frame   string `json:"frame"`
	Title   string `json:"title"`
	ICP     string `json:"icp"`
}

type pyGeneric struct {
	IP     string `json:"ip"`
	Port   string `json:"port"`
	URL    string `json:"url"`
	Host   string `json:"host"`
	Source string `json:"source"`
}

func uncoverPython(src string, results []sources.Result) any {
	switch src {
	case "fofa":
		out := make([]pyFofa, 0, len(results))
		for _, r := range results {
			var raw engine.RawFofa
			_ = json.Unmarshal(r.Raw, &raw)
			out = append(out, pyFofa{
				IP: raw.IP, Port: raw.Port, URL: raw.Host,
				Domain: raw.Domain, Title: raw.Title, ICP: raw.ICP,
			})
		}
		return out
	case "hunter":
		out := make([]pyHunter, 0, len(results))
		for _, r := range results {
			var raw engine.RawHunter
			_ = json.Unmarshal(r.Raw, &raw)
			out = append(out, pyHunter{
				IP: raw.IP, Port: raw.Port, URL: raw.URL,
				Domain: raw.Domain, Status: raw.Status,
				Company: raw.Company, Frame: raw.Frame,
				Title: raw.Title, ICP: raw.ICP,
			})
		}
		return out
	default:
		out := make([]pyGeneric, 0, len(results))
		for _, r := range results {
			out = append(out, pyGeneric{
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
	return out
}
