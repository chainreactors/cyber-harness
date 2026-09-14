package console

import (
	"net/url"
	"strings"
)

// redactIOAURL strips the access token that the IOA URL carries as userinfo
// (http://<token>@host/ioa) so /status never prints the secret to the terminal
// or into a shared screenshot. If URL parsing fails it still conservatively
// strips userinfo from a scheme://userinfo@host authority; token-less URLs are
// returned unchanged.
func redactIOAURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return redactURLUserinfoFallback(raw)
	}
	if u.User == nil {
		return redactURLUserinfoFallback(raw)
	}
	u.User = nil
	return u.String()
}

func redactURLUserinfoFallback(raw string) string {
	scheme := strings.Index(raw, "://")
	if scheme < 0 {
		return raw
	}
	authorityStart := scheme + len("://")
	authorityEnd := len(raw)
	if rel := strings.IndexAny(raw[authorityStart:], "/?#"); rel >= 0 {
		authorityEnd = authorityStart + rel
	}
	at := strings.LastIndex(raw[authorityStart:authorityEnd], "@")
	if at < 0 {
		return raw
	}
	return raw[:authorityStart] + raw[authorityStart+at+1:]
}
