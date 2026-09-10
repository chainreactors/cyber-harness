package console

import (
	"github.com/spf13/cobra"
	"net/url"
	"strings"
	"time"
)

type SavedSession struct {
	Path      string
	SessionID string
	Model     string
	Messages  int
	UpdatedAt time.Time
}

func (s SavedSession) SortTime() time.Time { return s.UpdatedAt }
func (r *AgentConsole) skillCommands() []*cobra.Command {
	var cmds []*cobra.Command
	for _, skill := range r.runtime.App().Skills.Skills {
		if strings.TrimSpace(skill.Name) == "" || skill.Internal {
			continue
		}
		cmds = append(cmds, &cobra.Command{Use: "/skill:" + skill.Name, Short: skill.Description, DisableFlagParsing: true,
			RunE: func(c *cobra.Command, args []string) error {
				return r.submitPrompt(c.Name()+" "+strings.Join(args, " "), false)
			}})
	}
	return cmds
}

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
