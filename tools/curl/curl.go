package curl

import (
	"context"
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/telemetry"
	coretool "github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/toolargs"
)

// Command is a pure-Go, evidence-first reimplementation of curl. It exposes a
// curl-shaped flag surface so the agent uses it exactly as it would use system
// curl (this command shadows the system binary), while every request routes
// through the runner's MITM hub — attributed by tool-call id and captured as
// http.exchange evidence — and carries a browser-shaped header set by default
// instead of announcing itself as a scanner.
type Command struct {
	toolargs.Base
}

func New() *Command {
	c := &Command{}
	c.InitLogger(nil)
	return c
}

func (c *Command) WithLogger(logger telemetry.Logger) *Command {
	c.InitLogger(logger)
	return c
}

func (c *Command) WithProxy(proxy string) *Command {
	c.Proxy = proxy
	return c
}

func (c *Command) WithEvents(events aop.EventEmitter) *Command {
	c.Events = events
	return c
}

func (c *Command) Name() string { return "curl" }

func (c *Command) Usage() string {
	return `curl - transfer a URL (pure-Go, browser-naturalized, evidence-first)

Usage: curl [options] <url>

Supported options:
  -X, --request <method>       HTTP method
  -H, --header <line>          Extra header ("Name: value"); repeatable
  -d, --data <data>            POST body (@file to read a file); --data-raw / --data-binary
  -G, --get                    Send -d data as a query string
  -b, --cookie <data|file>     Cookie string ("k=v") or file to read
  -c, --cookie-jar <file>      Write cookies to this file after the exchange
  -L, --location               Follow redirects   (--max-redirs <n>)
  -A, --user-agent <ua>        Override the User-Agent
  -e, --referer <url>          Referer header
  -u, --user <user:password>   Basic authentication
  -o, --output <file>          Write body to file instead of stdout
  -i, --include                Include response headers in the output
  -s, --silent                 Silent mode
  -S, --show-error             Show errors even with -s
  -w, --write-out <format>     After completion, print %{http_code}, %{url_effective}, ...
  -v, --verbose                Log request/response headers
  -k, --insecure               Do not verify TLS
      --connect-timeout <s>    Connection timeout, seconds
      --max-time <s>           Overall timeout, seconds

Unlisted flags are rejected rather than silently ignored. Requests are routed
through the runner proxy and recorded as HTTP evidence; a browser User-Agent and
header set are applied unless you override them.`
}

func (c *Command) QuickReference() string {
	return `### curl — HTTP requests (pure-Go, browser-naturalized, evidence-first)
  curl <url>                     GET a URL
  curl -X POST -d 'a=1' <url>    POST form data
  curl -H 'Authorization: ...' <url>
  curl -i -L <url>               Include headers, follow redirects
  curl -b 'sid=abc' -c jar.txt <url>   Send and persist cookies`
}

// Run parses the curl-shaped argument vector and performs one exchange. The
// proxy and CA that route/trust the MITM hub arrive in execution.Env (the
// builtin runs in-process and does not inherit them from os.Environ).
func (c *Command) Run(ctx context.Context, execution *commands.Execution) (_ any, err error) {
	defer telemetry.RecoverAsError("curl", &err)

	req, err := Parse(execution.Args)
	if err != nil {
		return nil, err
	}

	workDir := execution.Dir
	if workDir == "" {
		workDir = coretool.WorkDirFromContext(ctx, c.WorkDir)
	}
	return nil, c.do(ctx, req, envMap(execution.Env), workDir, execution.Stdout, execution.Stderr)
}

func envMap(env []string) map[string]string {
	values := make(map[string]string, len(env))
	for _, item := range env {
		if key, value, ok := strings.Cut(item, "="); ok {
			values[key] = value
		}
	}
	return values
}
