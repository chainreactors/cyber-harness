package aopws

import (
	"fmt"
	"net/url"
	"strings"
)

// DialURL builds the WebSocket endpoint and extracts an access token from URL userinfo.
func DialURL(serverURL, path string) (string, string, error) {
	u, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("invalid AOP server URL %q", serverURL)
	}
	token := ""
	if u.User != nil {
		token = u.User.Username()
		u.User = nil
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", "", fmt.Errorf("unsupported AOP server URL scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(path, "/")
	u.RawPath = ""
	u.Fragment = ""
	return u.String(), token, nil
}
