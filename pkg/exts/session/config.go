package session

import (
	"time"

	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/pkg/config"
)

// ConfigFromOption projects user parameters onto an existing product selection.
func ConfigFromOption(option *config.Option, selected session.Config) session.Config {
	if option == nil {
		return selected
	}
	selected.Heartbeat = time.Duration(option.Heartbeat) * time.Minute
	selected.Resume = option.Resume
	selected.SelectedSkills = append([]string(nil), option.Skills...)
	selected.CaptureProviderFrames = option.CaptureProviderFrames
	return selected
}
