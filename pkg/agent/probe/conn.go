// Package probe verifies connectivity to aiscan's external dependencies
// (cyberhub, recon providers, search, IOA, and the LLM) using a supplied config.
// Probe failures are reported inside the result structs rather than as returned
// errors; a returned error only signals an unknown/untestable section.
package probe

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	ioaclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/sdk/pkg/cyberhub"

	"github.com/chainreactors/aiscan/pkg/tools/search"
)

// ConnCheck is the outcome of probing one external dependency. A single
// settings section may run more than one check (Recon probes FOFA and Hunter
// independently), so callers always receive a list and the UI renders each row.
type ConnCheck struct {
	Name      string `json:"name"` // fofa, hunter, cyberhub, tavily, ioa
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latency_ms"`
	Detail    string `json:"detail,omitempty"`
	Error     string `json:"error,omitempty"`
}

// connProbeTimeout bounds a single connectivity check so an unreachable or
// misconfigured endpoint fails fast instead of hanging the settings dialog.
const connProbeTimeout = 20 * time.Second

// Recon provider endpoints. Declared as vars (not consts) so tests can point
// them at a local stub server.
var (
	FofaInfoEndpoint     = "https://fofa.info/api/v1/info/my"
	HunterSearchEndpoint = "https://hunter.qianxin.com/openApi/search"
)

// TestConn probes the external dependencies of one settings section using the
// supplied (possibly partial) config. Secret fields left blank fall back to the
// value already stored on the server (stored), mirroring the settings UI
// convention where a configured secret is left empty to keep it unchanged. Like
// TestLLM, probe failures are reported inside ConnCheck rather than as a
// returned error; a non-nil error only signals an unknown/untestable section.
func TestConn(ctx context.Context, section string, in, stored ProbeConfig) ([]ConnCheck, error) {
	switch strings.ToLower(strings.TrimSpace(section)) {
	case "cyberhub":
		return testCyberhub(ctx, in, stored), nil
	case "recon":
		return testRecon(ctx, in, stored), nil
	case "search":
		return testSearch(ctx, in, stored), nil
	case "ioa":
		return testIOA(ctx, in, stored), nil
	default:
		return nil, fmt.Errorf("section %q has no connection to test", section)
	}
}

// --- section probes ---

func testCyberhub(ctx context.Context, in, stored ProbeConfig) []ConnCheck {
	hubURL := fallbackStr(in.Cyberhub.URL, stored.Cyberhub.URL)
	key := fallbackStr(in.Cyberhub.Key, stored.Cyberhub.Key)
	return []ConnCheck{runCheck("cyberhub", func() (string, error) {
		if strings.TrimSpace(hubURL) == "" {
			return "", fmt.Errorf("cyberhub url is empty")
		}
		probeCtx, cancel := context.WithTimeout(ctx, connProbeTimeout)
		defer cancel()
		provider := cyberhub.NewProvider(hubURL, key).
			WithTimeout(connProbeTimeout).
			WithFilter(&cyberhub.ExportFilter{Limit: 1})
		fingers, _, err := provider.Fingers(probeCtx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("reachable · %d fingerprint(s) sampled", len(fingers)), nil
	})}
}

func testRecon(ctx context.Context, in, stored ProbeConfig) []ConnCheck {
	proxy := fallbackStr(in.Recon.Proxy, stored.Recon.Proxy)
	var checks []ConnCheck

	if fofaKey := fallbackStr(in.Recon.FofaKey, stored.Recon.FofaKey); strings.TrimSpace(fofaKey) != "" {
		checks = append(checks, runCheck("fofa", func() (string, error) {
			return probeFofa(ctx, fofaKey, proxy)
		}))
	}

	// Hunter accepts either an API key or a (legacy, rarely used) web token; the
	// API key takes precedence, matching the recon engine's credential order.
	hunterKey := fallbackStr(in.Recon.HunterAPIKey, stored.Recon.HunterAPIKey)
	if strings.TrimSpace(hunterKey) == "" {
		hunterKey = fallbackStr(in.Recon.HunterToken, stored.Recon.HunterToken)
	}
	if strings.TrimSpace(hunterKey) != "" {
		checks = append(checks, runCheck("hunter", func() (string, error) {
			return probeHunter(ctx, hunterKey, proxy)
		}))
	}

	if len(checks) == 0 {
		checks = append(checks, ConnCheck{Name: "recon", Error: "no FOFA or Hunter credentials configured"})
	}
	return checks
}

func testSearch(ctx context.Context, in, stored ProbeConfig) []ConnCheck {
	keys := fallbackStr(in.Search.TavilyKeys, stored.Search.TavilyKeys)
	return []ConnCheck{runCheck("tavily", func() (string, error) {
		first := firstCSV(keys)
		if first == "" {
			return "", fmt.Errorf("no tavily api key configured")
		}
		probeCtx, cancel := context.WithTimeout(ctx, connProbeTimeout)
		defer cancel()
		return search.ProbeTavily(probeCtx, first, "")
	})}
}

func testIOA(ctx context.Context, in, stored ProbeConfig) []ConnCheck {
	ioaURL := fallbackStr(in.IOA.URL, stored.IOA.URL)
	token := fallbackStr(in.IOA.Token, stored.IOA.Token)
	return []ConnCheck{runCheck("ioa", func() (string, error) {
		if strings.TrimSpace(ioaURL) == "" {
			return "", fmt.Errorf("ioa url is empty")
		}
		client, err := newIOAProbeClient(ioaURL, token)
		if err != nil {
			return "", err
		}
		probeCtx, cancel := context.WithTimeout(ctx, connProbeTimeout)
		defer cancel()
		// ListSpaces is a read-only authenticated call: it proves the server is
		// reachable and the token is accepted without the side effect of
		// registering a node (which EnsureRegistered would do on every click).
		spaces, err := client.ListSpaces(probeCtx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("connected · %d space(s)", len(spaces)), nil
	})}
}

// --- provider probes ---

// redactURLError strips the query string from a *url.Error's URL so a secret
// carried as a query parameter (e.g. a FOFA/Hunter API key) is never surfaced
// in an error message. Non-url.Error values pass through unchanged.
func redactURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if i := strings.IndexByte(ue.URL, '?'); i >= 0 {
			ue.URL = ue.URL[:i] + "?<redacted>"
		}
	}
	return err
}

// probeGetJSON issues a bounded GET to endpoint through the (optionally proxied)
// probe client, returning the response body and failing on any non-200 status.
func probeGetJSON(ctx context.Context, endpoint, proxy string) ([]byte, error) {
	probeCtx, cancel := context.WithTimeout(ctx, connProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if strings.TrimSpace(proxy) != "" {
		if proxyURL, err := url.Parse(proxy); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	client := &http.Client{Timeout: connProbeTimeout, Transport: transport}

	resp, err := client.Do(req)
	if err != nil {
		// The provider key rides in the query string (FOFA/Hunter have no
		// header/body auth), and *url.Error.Error() echoes the full URL — which
		// then flows into ConnCheck.Error and back to the client/logs. Strip the
		// query before surfacing so the (write-only) key never leaks.
		return nil, redactURLError(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		s := strings.TrimSpace(string(body))
		if len(s) > 200 {
			s = s[:200] + "…"
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, s)
	}
	return body, nil
}

// probeFofa validates a FOFA key via the account-info endpoint, which checks
// the credential without consuming a search quota.
func probeFofa(ctx context.Context, key, proxy string) (string, error) {
	body, err := probeGetJSON(ctx, FofaInfoEndpoint+"?key="+url.QueryEscape(key), proxy)
	if err != nil {
		return "", err
	}

	var r struct {
		Error     bool        `json:"error"`
		Errmsg    string      `json:"errmsg"`
		Email     string      `json:"email"`
		Username  string      `json:"username"`
		FofaPoint json.Number `json:"fofa_point"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("parse FOFA response: %w", err)
	}
	if r.Error {
		if r.Errmsg != "" {
			return "", fmt.Errorf("%s", r.Errmsg)
		}
		return "", fmt.Errorf("FOFA rejected the key")
	}
	who := r.Username
	if who == "" {
		who = r.Email
	}
	if who == "" {
		who = "key valid"
	}
	if r.FofaPoint != "" {
		return fmt.Sprintf("%s · %s points", who, r.FofaPoint.String()), nil
	}
	return who, nil
}

// probeHunter validates a Hunter key with the smallest possible search. Hunter
// has no free account-info endpoint, so this consumes one minimal query.
func probeHunter(ctx context.Context, key, proxy string) (string, error) {
	now := time.Now()
	params := url.Values{}
	params.Set("api-key", key)
	params.Set("search", base64.URLEncoding.EncodeToString([]byte(`ip="1.1.1.1"`)))
	params.Set("page", "1")
	params.Set("page_size", "1")
	params.Set("is_web", "3")
	params.Set("start_time", now.AddDate(0, 0, -1).Format("2006-01-02 15:04:05"))
	params.Set("end_time", now.Format("2006-01-02 15:04:05"))

	body, err := probeGetJSON(ctx, HunterSearchEndpoint+"?"+params.Encode(), proxy)
	if err != nil {
		return "", err
	}

	var r struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("parse Hunter response: %w", err)
	}
	if r.Code != 200 {
		if r.Message != "" {
			return "", fmt.Errorf("code %d: %s", r.Code, r.Message)
		}
		return "", fmt.Errorf("Hunter returned code %d", r.Code)
	}
	return fmt.Sprintf("key valid · %d total", r.Data.Total), nil
}

func newIOAProbeClient(rawURL, token string) (*ioaclient.Client, error) {
	if strings.TrimSpace(token) != "" {
		return ioaclient.NewClientWithToken(rawURL, token)
	}
	// No explicit token: NewClient still extracts an access key from a
	// userinfo-style URL (http://token@host:port).
	return ioaclient.NewClient(rawURL, "")
}

// --- helpers ---

// runCheck times fn and folds its outcome into a ConnCheck.
func runCheck(name string, fn func() (string, error)) ConnCheck {
	start := time.Now()
	detail, err := fn()
	c := ConnCheck{Name: name, LatencyMs: time.Since(start).Milliseconds()}
	if err != nil {
		c.Error = err.Error()
		return c
	}
	c.OK = true
	c.Detail = detail
	return c
}

// fallbackStr returns in when non-blank, otherwise the stored value.
func fallbackStr(in, stored string) string {
	if strings.TrimSpace(in) != "" {
		return in
	}
	return stored
}

func firstCSV(s string) string {
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			return p
		}
	}
	return ""
}

