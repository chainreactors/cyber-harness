// Package browser centralizes browser binary discovery for AIScan's browser-backed engines.
package browser

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/go-rod/rod/lib/launcher"
)

const (
	// PathEnv explicitly selects the Chrome-compatible browser binary used by AIScan.
	PathEnv = "AISCAN_BROWSER_PATH"
)

// Source identifies how a browser binary was selected.
type Source string

const (
	SourceEnvironment Source = "environment"
	SourceSystem      Source = "system"
)

// Binary describes a discovered Chrome-compatible browser executable.
type Binary struct {
	Path   string
	Source Source
}

// Discover resolves the browser shared by Playwright, nuclei headless, and Katana.
// An explicit AISCAN_BROWSER_PATH is authoritative. If neither it nor a system
// browser is available, an empty result lets Rod use its cached/download fallback.
func Discover() (Binary, error) {
	configured, configuredSet := os.LookupEnv(PathEnv)
	return discover(configured, configuredSet, exec.LookPath, launcher.LookPath)
}

func discover(
	configured string,
	configuredSet bool,
	resolve func(string) (string, error),
	findSystem func() (string, bool),
) (Binary, error) {
	configured = strings.TrimSpace(configured)
	if configuredSet && configured != "" {
		path, err := resolve(configured)
		if err != nil {
			return Binary{}, fmt.Errorf("%s=%q does not resolve to an executable browser: %w", PathEnv, configured, err)
		}
		return Binary{Path: path, Source: SourceEnvironment}, nil
	}

	if path, ok := findSystem(); ok && path != "" {
		return Binary{Path: path, Source: SourceSystem}, nil
	}
	return Binary{}, nil
}
