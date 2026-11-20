package passive

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/projectdiscovery/uncover/sources"
)

// Each query owns its session, so rewriting its transports also covers SDKs
// (including Censys) without changing package globals or other invocations.
func redirect(session *sources.Session, baseURL string) error {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return fmt.Errorf("invalid passive base_url")
	}
	for _, client := range []*http.Client{session.Client.HTTPClient, session.Client.HTTPClient2} {
		if client == nil {
			continue
		}
		client.Transport = &transport{base: u, next: client.Transport}
		// A redirected response must not escape the selected upstream or append
		// the configured prefix twice. Native API errors reach the caller.
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return nil
}

type transport struct {
	base *url.URL
	next http.RoundTripper
}

func (t *transport) RoundTrip(request *http.Request) (*http.Response, error) {
	next := request.Clone(request.Context())
	next.URL.Scheme, next.URL.Host = t.base.Scheme, t.base.Host
	next.URL.Path = strings.TrimRight(t.base.Path, "/") + "/" + strings.TrimLeft(request.URL.Path, "/")
	next.URL.RawPath = strings.TrimRight(t.base.EscapedPath(), "/") + "/" + strings.TrimLeft(request.URL.EscapedPath(), "/")
	next.Host = ""
	return t.next.RoundTrip(next)
}

func (t *transport) CloseIdleConnections() {
	if closer, ok := t.next.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
