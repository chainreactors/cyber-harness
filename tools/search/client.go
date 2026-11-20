package search

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/cyber/core/truncate"
)

const (
	searchTimeout   = 30 * time.Second
	maxResponseBody = truncate.MaxResponseBody

	defaultNumResults = 5
	maxNumResults     = 10
)

// Client searches through the configured Tavily and DuckDuckGo upstreams.
type Client struct {
	config     Config
	client     *http.Client
	apiKeys    []string
	currentKey int
	mu         sync.Mutex
}

// Config scopes upstream selection to this search client.
type Config struct {
	Keys          string
	BaseURL       string
	DDGBaseURL    string
	DisableTavily bool
	DisableDDG    bool
	Proxy         string
}

func New(config Config) *Client {
	c := &Client{
		config: config,
		client: &http.Client{
			Timeout:   searchTimeout,
			Transport: proxyTransport(config.Proxy),
		},
	}
	if config.BaseURL != "" || config.DDGBaseURL != "" {
		c.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}

	seen := make(map[string]struct{})
	if !config.DisableTavily {
		for _, k := range strings.Split(config.Keys, ",") {
			k = strings.TrimSpace(k)
			if k == "" {
				continue
			}
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			c.apiKeys = append(c.apiKeys, k)
		}
	}
	return c
}

// proxyTransport builds an HTTP transport from the configured egress. It never
// consults the process environment: an invocation's route must not be replaced
// by ambient proxy state, and malformed routes fail closed in Transport.Proxy.
func proxyTransport(proxy string) *http.Transport {
	t := &http.Transport{}
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return t
	}
	u, err := url.Parse(proxy)
	if err != nil || u.Scheme == "" || u.Host == "" {
		proxyErr := err
		if proxyErr == nil {
			proxyErr = fmt.Errorf("expected URL with scheme and host")
		}
		t.Proxy = func(*http.Request) (*url.URL, error) {
			return nil, fmt.Errorf("invalid proxy %q: %w", proxy, proxyErr)
		}
		return t
	}
	t.Proxy = http.ProxyURL(u)
	return t
}

func (c *Client) rotateKey() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.apiKeys) <= 1 {
		return false
	}
	c.currentKey = (c.currentKey + 1) % len(c.apiKeys)
	return true
}

// currentAPIKey snapshots the active key index and value under the lock. The
// active index changes during concurrent searches; the key list is immutable.
func (c *Client) currentAPIKey() (int, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentKey, c.apiKeys[c.currentKey]
}

func (c *Client) Execute(ctx context.Context, args []string) (string, error) {
	query, num, err := parseTavilyArgs(args)
	if err != nil {
		return "", err
	}

	if len(c.apiKeys) == 0 {
		return c.searchDDG(ctx, query, num)
	}
	startKey, key := c.currentAPIKey()
	var failure error
	for {
		result, err := c.searchTavily(ctx, query, num, key)
		if err == nil {
			return result, nil
		}
		failure = err
		if !isKeyExhausted(err) {
			break
		}
		if !c.rotateKey() {
			break
		}
		var idx int
		if idx, key = c.currentAPIKey(); idx == startKey {
			break
		}
	}
	if c.config.BaseURL != "" || c.config.DisableDDG {
		return "", failure
	}
	fallback, fbErr := c.searchDDG(ctx, query, num)
	if fbErr != nil {
		return "", fmt.Errorf("tavily keys exhausted; ddg fallback: %w", fbErr)
	}
	return fallback, nil
}

func parseTavilyArgs(args []string) (query string, num int, err error) {
	num = defaultNumResults

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--num":
			i++
			if i >= len(args) {
				return "", 0, fmt.Errorf("search tavily: --num requires a value")
			}
			value := args[i] //nolint:gosec // G602: bounds checked by i >= len(args) above
			n, parseErr := strconv.Atoi(value)
			if parseErr != nil {
				return "", 0, fmt.Errorf("search tavily: invalid --num value: %s", value)
			}
			num = n
		default:
			if strings.HasPrefix(args[i], "--") {
				return "", 0, fmt.Errorf("search tavily: unknown flag: %s", args[i])
			}
			if query == "" {
				query = args[i]
			} else {
				query += " " + args[i]
			}
		}
	}

	if query == "" {
		return "", 0, fmt.Errorf("search tavily: query is required\n\nUsage: search tavily <query> [--num <N>]")
	}
	if num <= 0 {
		num = defaultNumResults
	}
	if num > maxNumResults {
		num = maxNumResults
	}
	return query, num, nil
}

func isKeyExhausted(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "HTTP 401") ||
		strings.Contains(msg, "HTTP 429") ||
		strings.Contains(msg, "HTTP 403")
}
