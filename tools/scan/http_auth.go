package scan

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	sdkzombie "github.com/chainreactors/sdk/zombie"
	"github.com/chainreactors/utils"
)

var basicAuthChallengePattern = regexp.MustCompile(`(?i)(^|,)\s*basic(\s|$)`)

func basicAuthZombieTarget(ctx context.Context, rawURL, hostHeader string, timeoutSeconds int) (sdkzombie.Target, bool) {
	parsed, ok := parseInputURL(rawURL)
	if !ok || !utils.IsWebScheme(parsed.Scheme) {
		return sdkzombie.Target{}, false
	}
	if !hasHTTPBasicAuthChallenge(ctx, parsed, hostHeader, timeoutSeconds) {
		return sdkzombie.Target{}, false
	}

	target, ok := zombieTargetFromParsedURL(parsed, "")
	if !ok || !isGenericWebZombieService(target.Service) {
		return sdkzombie.Target{}, false
	}
	target.Param = basicAuthParams(parsed, hostHeader)
	return target, true
}

func hasHTTPBasicAuthChallenge(ctx context.Context, parsed *url.URL, hostHeader string, timeoutSeconds int) bool {
	if parsed == nil {
		return false
	}
	probeURL := *parsed
	probeURL.User = nil
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL.String(), nil)
	if err != nil {
		return false
	}
	if hostHeader != "" {
		req.Host = hostHeader
	}
	req.Header.Set("User-Agent", "aiscan")
	req.Close = true

	client := httpAuthClient(timeoutSeconds)
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusUnauthorized && hasBasicAuthChallenge(resp.Header.Values("WWW-Authenticate"))
}

func httpAuthClient(timeoutSeconds int) *http.Client {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 5
	}
	transport := http.DefaultTransport.(*http.Transport).Clone() //nolint:errcheck // DefaultTransport is always *http.Transport
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // scanner probes must tolerate self-signed certs
	return &http.Client{
		Timeout:   time.Duration(timeoutSeconds) * time.Second,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func hasBasicAuthChallenge(values []string) bool {
	for _, value := range values {
		if basicAuthChallengePattern.MatchString(value) {
			return true
		}
	}
	return false
}

func basicAuthParams(parsed *url.URL, hostHeader string) map[string]string {
	params := make(map[string]string)
	path := strings.TrimPrefix(parsed.EscapedPath(), "/")
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	if path != "" {
		params["path"] = path
	}
	if hostHeader != "" {
		params["host"] = strings.ToLower(strings.TrimSpace(hostHeader))
	}
	if len(params) == 0 {
		return nil
	}
	return params
}
