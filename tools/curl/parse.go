package curl

import (
	"fmt"
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
	URL       string
	Method    string
	Headers   []Header
	Data      []DataPart
	Form      []FormPart
	Get       bool // -G: send data as query string
	Follow    bool // -L
	MaxRedirs int  // --max-redirs (default 50 when following)

	UserAgent string // -A
	Referer   string // -e
	User      string // -u user[:password]
	CookieIn  string // -b: cookie string or @file
	CookieJar string // -c: write jar here after the exchange

	Output    string // -o
	Include   bool   // -i
	Silent    bool   // -s
	ShowError bool   // -S
	WriteOut  string // -w
	Verbose   bool   // -v
	Insecure  bool   // -k
	Proxy     string // -x: override the egress proxy for this invocation

	ConnectTimeout time.Duration // --connect-timeout
	MaxTime        time.Duration // --max-time
}

// valueShort maps short flags that consume a value.
var valueShort = map[byte]string{
	'X': "request", 'H': "header", 'd': "data", 'b': "cookie", 'c': "cookie-jar",
	'A': "user-agent", 'e': "referer", 'u': "user", 'o': "output", 'w': "write-out",
	'F': "form", 'x': "proxy",
}

// boolShort maps short flags that take no value.
var boolShort = map[byte]string{
	'G': "get", 'L': "location", 'i': "include", 's': "silent",
	'S': "show-error", 'v': "verbose", 'k': "insecure",
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
		if (len(r.Data) > 0 || len(r.Form) > 0) && !r.Get {
			r.Method = "POST"
		} else {
			r.Method = "GET"
		}
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
	case "include":
		r.Include = true
	case "silent":
		r.Silent = true
	case "show-error":
		r.ShowError = true
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
	if err != nil || f < 0 {
		return 0, fmt.Errorf("expects a non-negative number of seconds: %q", v)
	}
	return time.Duration(f * float64(time.Second)), nil
}
