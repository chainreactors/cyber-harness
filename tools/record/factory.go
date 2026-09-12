//go:build full && record_ffmpeg && cgo && (windows || linux)

package record

import (
	coreconfig "github.com/chainreactors/aiscan/core/config"
	"os"
)

// NewConfigured constructs the platform recorder using the existing environment policy.
func NewConfigured(workDir string) (*Tool, error) {
	maximum, err := maxConcurrentFromEnvironment(os.LookupEnv)
	if err != nil {
		return nil, err
	}
	return New(workDir, coreconfig.DataSubDir("record"), maximum, newPlatformBackend()), nil
}
