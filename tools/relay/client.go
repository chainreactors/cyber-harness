package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// A captcha solve may take the relay's full 150-second limit.
	defaultTimeout = 180 * time.Second
	maxResponse    = 16 << 20
)

// Config selects one relay and the key every authenticated call sends.
type Config struct {
	BaseURL string
	Key     string
	// HTTPClient carries the route and timeout. Redirects are never followed,
	// so the key is not sent to another origin.
	HTTPClient *http.Client
}

type Client struct {
	base string
	key  string
	http *http.Client
}

func New(config Config) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(config.BaseURL), "/"))
	if err != nil || base.Hostname() == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" {
		return nil, fmt.Errorf("relay base_url must be an HTTP(S) URL without credentials, query or fragment")
	}
	key := strings.TrimSpace(config.Key)
	if key == "" {
		return nil, fmt.Errorf("relay key is required")
	}
	client := &http.Client{Timeout: defaultTimeout}
	if config.HTTPClient != nil {
		copied := *config.HTTPClient
		client = &copied
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: base.String(), key: key, http: client}, nil
}

// Error is a failed call: the relay's error envelope plus the HTTP status.
// Act on Code and follow Hint when present; never parse Message.
type Error struct {
	Status     int
	RetryAfter time.Duration
	RequestID  string
	RelayError
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "relay: HTTP %d", e.Status)
	if e.Code != "" {
		b.WriteString(" " + e.Code)
	}
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	if e.Hint != "" {
		b.WriteString("; hint: " + e.Hint)
	}
	if e.RetryAfter > 0 {
		fmt.Fprintf(&b, "; retry after %s", e.RetryAfter)
	}
	if e.RequestID != "" {
		b.WriteString(" [request " + e.RequestID + "]")
	}
	return b.String()
}

// do sends one call. path is already escaped; auth reports whether the
// operation needs the key (the probes do not).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any, auth bool) error {
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("relay %s %s: invalid request path", method, path)
	}
	for _, seg := range strings.Split(path[1:], "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("relay %s %s: invalid path segment %q", method, path, seg)
		}
	}
	target := c.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("relay: encode request: %w", err)
		}
		body = bytes.NewReader(data)
	}
	// #nosec G704 -- The host configures the relay origin; caller values are path-escaped segments under it.
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return fmt.Errorf("relay: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	// #nosec G704 -- The destination is host-owned and redirects are disabled, so the key is never forwarded.
	resp, err := c.http.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("relay %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return fmt.Errorf("relay %s %s: read response: %w", method, path, err)
	}
	if len(data) > maxResponse {
		return fmt.Errorf("relay %s %s: response exceeds %d bytes", method, path, maxResponse)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		failure := &Error{Status: resp.StatusCode, RequestID: resp.Header.Get("X-Relay-Request-ID")}
		var envelope struct {
			Error *RelayError `json:"error"`
		}
		switch {
		case json.Unmarshal(data, &envelope) == nil && envelope.Error != nil:
			failure.RelayError = *envelope.Error
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			failure.Message = "unexpected redirect"
		default:
			failure.Message = strings.TrimSpace(string(data[:min(len(data), 512)]))
		}
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
			failure.RetryAfter = time.Duration(seconds) * time.Second
		}
		return failure
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("relay %s %s: decode response: %w", method, path, err)
	}
	return nil
}
