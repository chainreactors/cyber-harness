package curl

import (
	"context"
	"fmt"
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

// compatibilityVersion is intentionally explicit instead of inheriting the
// host's curl version. Agents can therefore use --version to discover that
// they are talking to the deterministic in-process implementation.
const compatibilityVersion = "curl 8.14.1 (aiscan pure-Go)"

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
      --data-urlencode <data>  Like -d, but percent-encode the content (name=content, name@file)
  -F, --form <name=content>    Multipart form field; name=@file uploads a file (;type=mime), name=<file reads text
  -G, --get                    Send -d data as a query string
  -b, --cookie <data|file>     Cookie string ("k=v") or file to read
  -c, --cookie-jar <file>      Write cookies to this file after the exchange
  -L, --location               Follow redirects   (--max-redirs <n>)
  -A, --user-agent <ua>        Override the User-Agent
  -e, --referer <url>          Referer header
  -u, --user <user:password>   Basic authentication
  -o, --output <file>          Write body to file instead of stdout
  -D, --dump-header <file>     Write response headers to a separate file
      --trace-ascii <file>     Write an ASCII trace (- stdout, % stderr)
  -i, --include                Include response headers in the output
  -I, --head                   Fetch headers only (HEAD request)
  -s, --silent                 Silent mode
  -S, --show-error             Show errors even with -s
  -f, --fail                   Fail on HTTP 4xx/5xx responses
  -w, --write-out <format>     After completion, print %{http_code}, %{url_effective}, ...
  -v, --verbose                Log request/response headers
  -N, --no-buffer              Stream response output without buffering
  -k, --insecure               Do not verify TLS
  -x, --proxy <url>            Use this proxy instead of the runner egress
      --connect-timeout <s>    Connection timeout, seconds
  -m, --max-time <s>           Overall timeout, seconds
      --http2                  Prefer HTTP/2
      --http1.1                Force HTTP/1.1
      --resolve host:port:addr Route a host/port to an explicit address
      --path-as-is             Preserve dot segments in the URL path
      --version, -V            Print the compatibility version and exit

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
  curl -I <url>                  HEAD request (headers only)
  curl -D headers.txt <url>      Save response headers separately
  curl -fsSL <url>               Follow redirects and fail on HTTP errors
  curl -b 'sid=abc' -c jar.txt <url>   Send and persist cookies
  curl -F 'file=@a.png' <url>    Multipart form upload`
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
	if req.Version {
		_, err := fmt.Fprintln(execution.Stdout, compatibilityVersion)
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
