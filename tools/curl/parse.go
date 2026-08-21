package curl

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Header is one -H value. Value is empty for a "Name;" send-empty form; Remove
// is set for a "Name:" form that suppresses a default header.
type Header struct {
	Name   string
	Value  string
	Remove bool
}

// DataPart is one -d/--data family value. File means the value is a path to
// read (@file); Binary keeps the bytes verbatim (--data-binary) while the
// default -d strips CR/LF from file contents; Raw disables @file handling
// (--data-raw); URLEncode percent-encodes the content (--data-urlencode).
type DataPart struct {
	Value     string
	File      bool
	Binary    bool
	Raw       bool
	URLEncode bool
}

// FormPart is one -F/--form value. File means Value is a path whose bytes
// become a file part (name=@path, optional ;type= MIME override); Content
// means Value is a path whose text becomes the field value (name=<path).
type FormPart struct {
	Name    string
	Value   string
	File    bool
	Content bool
	Type    string
}

// Request is the parsed, transport-agnostic shape of one curl invocation.
type Request struct {
	URL            string
	Method         string
	MethodExplicit bool // -X/--request was supplied; prevents -I changing it
	Headers        []Header
	Data           []DataPart
	Form           []FormPart
	Get            bool // -G: send data as query string
	Follow         bool // -L
	MaxRedirs      int  // --max-redirs (default 50 when following)

	UserAgent string // -A
	Referer   string // -e
	User      string // -u user[:password]
	CookieIn  string // -b: cookie string or @file
	CookieJar string // -c: write jar here after the exchange

	Output     string         // -o
	DumpHeader string         // -D: write response headers to a file (or - for stdout)
	Include    bool           // -i
	Head       bool           // -I/--head: issue a HEAD request and include headers
	Silent     bool           // -s
	ShowError  bool           // -S
	Fail       bool           // -f/--fail: fail on HTTP 4xx/5xx
	WriteOut   string         // -w
	Verbose    bool           // -v
	NoBuffer   bool           // -N/--no-buffer: stream response writes without buffering
	Insecure   bool           // -k
	Proxy      string         // -x: override the egress proxy for this invocation
	HTTP2      bool           // --http2: prefer/require HTTP/2 where available
	HTTP11     bool           // --http1.1: disable HTTP/2 negotiation
	PathAsIs   bool           // --path-as-is: preserve dot segments in the URL path
	Version    bool           // --version/-V: print the curl compatibility version
	Resolve    []ResolveEntry // --resolve host:port:address, repeatable

	ConnectTimeout time.Duration // --connect-timeout
	MaxTime        time.Duration // --max-time
}

// ResolveEntry describes one --resolve mapping. Addresses are tried in order
// when a target is dialled; the URL host (and therefore the HTTP Host and TLS
// SNI name) remains unchanged.
type ResolveEntry struct {
	Host      string
	Port      string
	Addresses []string
	Temporary bool // --resolve +host:... uses curl's temporary DNS lifetime
}

// valueShort maps short flags that consume a value.
var valueShort = map[byte]string{
	'X': "request", 'H': "header", 'd': "data", 'b': "cookie", 'c': "cookie-jar",
	'A': "user-agent", 'e': "referer", 'u': "user", 'o': "output", 'w': "write-out",
	'F': "form", 'x': "proxy", 'D': "dump-header", 'm': "max-time",
}

// boolShort maps short flags that take no value.
var boolShort = map[byte]string{
	'G': "get", 'L': "location", 'i': "include", 's': "silent",
	'S': "show-error", 'v': "verbose", 'k': "insecure", 'I': "head", 'f': "fail",
	'N': "no-buffer", 'V': "version",
}

// Parse turns a curl argument vector into a Request, rejecting any flag outside
// the supported set so the caller fails loudly rather than silently dropping an
// unsupported option.
func Parse(args []string) (*Request, error) {
	r := &Request{MaxRedirs: 50}
	var urls []string

	i := 0
	for i < len(args) {
		arg := args[i]
		i++
		switch {
		case arg == "--":
			urls = append(urls, args[i:]...)
			i = len(args)
		case strings.HasPrefix(arg, "--"):
			name := strings.TrimPrefix(arg, "--")
			var value string
			hasInline := false
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				value, hasInline = name[eq+1:], true
				name = name[:eq]
			}
			need := func() (string, error) {
				if hasInline {
					return value, nil
				}
				if i >= len(args) {
					return "", fmt.Errorf("curl: option --%s requires a value", name)
				}
				v := args[i]
				i++
				return v, nil
			}
			if err := r.applyLong(name, need); err != nil {
				return nil, err
			}
		case len(arg) > 1 && arg[0] == '-':
			// A bundle of short flags, e.g. -sSL, with an optional value-taking
			// flag last whose argument is attached or the next token.
			if err := r.applyShortBundle(arg[1:], args, &i); err != nil {
				return nil, err
			}
		default:
			urls = append(urls, arg)
		}
	}

	// --version is a local informational query and intentionally does not need
	// a URL (matching curl's `curl --version` behavior).
	if r.Version {
		// The native curl frontend exits after printing version information and
		// ignores any URL supplied alongside -V/--version.
		return r, nil
	}

	if r.URL == "" {
		switch len(urls) {
		case 0:
			return nil, fmt.Errorf("curl: no URL specified")
		case 1:
			r.URL = urls[0]
		default:
			return nil, fmt.Errorf("curl: multiple URLs are not supported yet (got %d)", len(urls))
		}
	} else if len(urls) > 0 {
		return nil, fmt.Errorf("curl: --url and a positional URL cannot both be given")
	}

	if r.Method == "" {
		if r.Head {
			r.Method = "HEAD"
		} else if (len(r.Data) > 0 || len(r.Form) > 0) && !r.Get {
			r.Method = "POST"
		} else {
			r.Method = "GET"
		}
	}
	if r.Head {
		r.Include = true
	}
	if len(r.Form) > 0 && len(r.Data) > 0 {
		return nil, fmt.Errorf("curl: (2) -d/--data and -F/--form cannot be combined")
	}
	if len(r.Form) > 0 && r.Get {
		return nil, fmt.Errorf("curl: (2) -G cannot be used with -F/--form")
	}
	return r, nil
}

func (r *Request) applyShortBundle(bundle string, args []string, i *int) error {
	for j := 0; j < len(bundle); j++ {
		ch := bundle[j]
		if long, ok := boolShort[ch]; ok {
			if err := r.applyLong(long, func() (string, error) {
				return "", fmt.Errorf("curl: -%c takes no value", ch)
			}); err != nil {
				return err
			}
			continue
		}
		if long, ok := valueShort[ch]; ok {
			// Value is the rest of the bundle when non-empty, else the next token.
			var value string
			if j+1 < len(bundle) {
				value = bundle[j+1:]
			} else {
				if *i >= len(args) {
					return fmt.Errorf("curl: option -%c requires a value", ch)
				}
				value = args[*i]
				*i++
			}
			return r.applyLong(long, func() (string, error) { return value, nil })
		}
		return fmt.Errorf("curl: unsupported flag -%c", ch)
	}
	return nil
}

func (r *Request) applyLong(name string, need func() (string, error)) error {
	switch name {
	case "url":
		v, err := need()
		if err != nil {
			return err
		}
		r.URL = v
	case "request":
		v, err := need()
		if err != nil {
			return err
		}
		r.Method = strings.ToUpper(v)
		r.MethodExplicit = true
	case "header":
		v, err := need()
		if err != nil {
			return err
		}
		r.Headers = append(r.Headers, parseHeader(v))
	case "data", "data-ascii":
		return r.addData(need, DataPart{})
	case "data-raw":
		return r.addData(need, DataPart{Raw: true})
	case "data-binary":
		return r.addData(need, DataPart{Binary: true})
	case "data-urlencode":
		return r.addData(need, DataPart{URLEncode: true})
	case "form":
		v, err := need()
		if err != nil {
			return err
		}
		part, err := parseFormPart(v)
		if err != nil {
			return err
		}
		r.Form = append(r.Form, part)
	case "get":
		r.Get = true
	case "location":
		r.Follow = true
	case "max-redirs":
		v, err := need()
		if err != nil {
			return err
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("curl: --max-redirs expects an integer: %q", v)
		}
		r.MaxRedirs = n
	case "user-agent":
		v, err := need()
		if err != nil {
			return err
		}
		r.UserAgent = v
	case "referer":
		v, err := need()
		if err != nil {
			return err
		}
		r.Referer = v
	case "user":
		v, err := need()
		if err != nil {
			return err
		}
		r.User = v
	case "cookie":
		v, err := need()
		if err != nil {
			return err
		}
		r.CookieIn = v
	case "cookie-jar":
		v, err := need()
		if err != nil {
			return err
		}
		r.CookieJar = v
	case "output":
		v, err := need()
		if err != nil {
			return err
		}
		r.Output = v
	case "dump-header":
		v, err := need()
		if err != nil {
			return err
		}
		r.DumpHeader = v
	case "include":
		r.Include = true
	case "head":
		r.Head = true
		r.Include = true
	case "silent":
		r.Silent = true
	case "show-error":
		r.ShowError = true
	case "fail":
		r.Fail = true
	case "write-out":
		v, err := need()
		if err != nil {
			return err
		}
		r.WriteOut = v
	case "verbose":
		r.Verbose = true
	case "insecure":
		r.Insecure = true
	case "no-buffer":
		r.NoBuffer = true
	case "proxy":
		v, err := need()
		if err != nil {
			return err
		}
		r.Proxy = v
	case "connect-timeout":
		v, err := need()
		if err != nil {
			return err
		}
		d, err := parseSeconds(v)
		if err != nil {
			return fmt.Errorf("curl: --connect-timeout %w", err)
		}
		r.ConnectTimeout = d
	case "max-time":
		v, err := need()
		if err != nil {
			return err
		}
		d, err := parseSeconds(v)
		if err != nil {
			return fmt.Errorf("curl: --max-time %w", err)
		}
		r.MaxTime = d
	case "http2":
		// curl treats repeated HTTP-version selectors as last-option-wins and
		// emits only a diagnostic on the native frontend. The parser has no
		// stderr channel, so retain the deterministic state without silently
		// ignoring either recognized option.
		r.HTTP11 = false
		r.HTTP2 = true
	case "http1.1":
		r.HTTP2 = false
		r.HTTP11 = true
	case "path-as-is":
		r.PathAsIs = true
	case "resolve":
		v, err := need()
		if err != nil {
			return err
		}
		entry, err := parseResolve(v)
		if err != nil {
			return err
		}
		r.Resolve = append(r.Resolve, entry)
	case "version":
		r.Version = true
	default:
		return fmt.Errorf("curl: unsupported flag --%s", name)
	}
	return nil
}

func (r *Request) addData(need func() (string, error), tpl DataPart) error {
	v, err := need()
	if err != nil {
		return err
	}
	part := tpl
	if !tpl.Raw && strings.HasPrefix(v, "@") {
		part.File = true
		part.Value = v[1:]
	} else {
		part.Value = v
	}
	r.Data = append(r.Data, part)
	return nil
}

// parseFormPart splits a -F value. curl requires name=content; @path attaches
// a file (with an optional ;type= MIME override), <path reads a file's text as
// the field value.
func parseFormPart(raw string) (FormPart, error) {
	name, value, ok := strings.Cut(raw, "=")
	if !ok || name == "" {
		return FormPart{}, fmt.Errorf("curl: (26) badly formatted form field %q, expected name=content", raw)
	}
	part := FormPart{Name: name}
	switch {
	case strings.HasPrefix(value, "@"):
		part.File = true
		value = value[1:]
		if idx := strings.Index(value, ";type="); idx >= 0 {
			part.Type = value[idx+len(";type="):]
			value = value[:idx]
		}
		part.Value = value
	case strings.HasPrefix(value, "<"):
		part.Content = true
		part.Value = value[1:]
	default:
		part.Value = value
	}
	return part, nil
}

// parseHeader splits a -H value into name/value, honoring curl's "Name;"
// (send empty) and "Name:" (remove default) forms.
func parseHeader(raw string) Header {
	if idx := strings.IndexByte(raw, ':'); idx >= 0 {
		name := strings.TrimSpace(raw[:idx])
		value := strings.TrimSpace(raw[idx+1:])
		if value == "" {
			return Header{Name: name, Remove: true}
		}
		return Header{Name: name, Value: value}
	}
	if idx := strings.IndexByte(raw, ';'); idx >= 0 {
		return Header{Name: strings.TrimSpace(raw[:idx]), Value: ""}
	}
	return Header{Name: strings.TrimSpace(raw)}
}

func parseSeconds(v string) (time.Duration, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("expects a non-negative number of seconds: %q", v)
	}
	nanos := f * float64(time.Second)
	// float64((1<<63)-1) rounds up to 1<<63. Use an inclusive bound so a
	// value that would wrap time.Duration into a negative duration is rejected
	// instead of silently turning into an effectively unbounded timeout.
	if nanos >= float64(1<<63) {
		return 0, fmt.Errorf("is too large: %q", v)
	}
	return time.Duration(nanos), nil
}

// parseResolve parses curl's host:port:address spelling. The address portion
// may itself contain colons (for example an IPv6 literal), so only the first
// two separators are significant. Multiple comma-separated addresses are
// accepted and tried in order by the dialer.
func parseResolve(raw string) (ResolveEntry, error) {
	raw = strings.TrimSpace(raw)
	temporaryEntry := strings.HasPrefix(raw, "+")
	if temporaryEntry {
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "+"))
	}
	host, rest, ok := splitResolveHost(raw)
	if !ok {
		return ResolveEntry{}, fmt.Errorf("curl: --resolve expects host:port:address: %q", raw)
	}
	second := strings.IndexByte(rest, ':')
	if second <= 0 || second == len(rest)-1 {
		return ResolveEntry{}, fmt.Errorf("curl: --resolve expects host:port:address: %q", raw)
	}
	port := strings.TrimSpace(rest[:second])
	addressSpec := strings.TrimSpace(rest[second+1:])
	if host == "" || port == "" || addressSpec == "" {
		return ResolveEntry{}, fmt.Errorf("curl: --resolve expects host:port:address: %q", raw)
	}
	if port != "*" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return ResolveEntry{}, fmt.Errorf("curl: --resolve has invalid port %q", port)
		}
	}
	addresses := make([]string, 0, 1)
	for _, value := range strings.Split(addressSpec, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		// Strip optional brackets from IPv6 literals; net.JoinHostPort adds
		// them back when constructing the dial target.
		value = strings.TrimPrefix(value, "[")
		value = strings.TrimSuffix(value, "]")
		addresses = append(addresses, value)
	}
	if len(addresses) == 0 {
		return ResolveEntry{}, fmt.Errorf("curl: --resolve has no address: %q", raw)
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return ResolveEntry{}, fmt.Errorf("curl: --resolve has no host: %q", raw)
	}
	return ResolveEntry{Host: strings.TrimSuffix(strings.ToLower(host), "."), Port: port, Addresses: addresses, Temporary: temporaryEntry}, nil
}

func splitResolveHost(raw string) (host, rest string, ok bool) {
	if strings.HasPrefix(raw, "[") {
		end := strings.IndexByte(raw, ']')
		if end < 0 || end+1 >= len(raw) || raw[end+1] != ':' {
			return "", "", false
		}
		return raw[:end+1], raw[end+2:], true
	}
	idx := strings.IndexByte(raw, ':')
	if idx <= 0 || idx == len(raw)-1 {
		return "", "", false
	}
	return raw[:idx], raw[idx+1:], true
}
