//go:build full && record_ffmpeg && cgo && (windows || linux)

package record

import "fmt"

// NewConfigured creates an inert recorder with host-resolved configuration.
func NewConfigured(workDir, directory string, maximum int) (*Tool, error) {
	if maximum < 1 || maximum > 16 {
		return nil, fmt.Errorf("record maximum must be between 1 and 16")
	}
	return New(workDir, directory, maximum, newPlatformBackend()), nil
}
