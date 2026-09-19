package console

import (
	"github.com/spf13/cobra"
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
	for _, skill := range r.runtime.Skills().All() {
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
