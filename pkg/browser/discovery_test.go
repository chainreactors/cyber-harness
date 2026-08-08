package browser

import (
	"errors"
	"strings"
	"testing"
)

func TestDiscoverPriority(t *testing.T) {
	tests := []struct {
		name          string
		configured    string
		configuredSet bool
		resolvePath   string
		resolveErr    error
		systemPath    string
		systemFound   bool
		want          Binary
		wantErr       bool
	}{
		{
			name:          "environment overrides system browser",
			configured:    " /opt/aiscan/chrome ",
			configuredSet: true,
			resolvePath:   "/opt/aiscan/chrome",
			systemPath:    "/usr/bin/chrome",
			systemFound:   true,
			want:          Binary{Path: "/opt/aiscan/chrome", Source: SourceEnvironment},
		},
		{
			name:          "invalid environment is an error",
			configured:    "/missing/chrome",
			configuredSet: true,
			resolveErr:    errors.New("not found"),
			systemPath:    "/usr/bin/chrome",
			systemFound:   true,
			wantErr:       true,
		},
		{
			name:        "system browser is automatic fallback",
			systemPath:  "/usr/bin/chromium",
			systemFound: true,
			want:        Binary{Path: "/usr/bin/chromium", Source: SourceSystem},
		},
		{
			name:          "blank environment still allows system discovery",
			configured:    "  ",
			configuredSet: true,
			systemPath:    "/usr/bin/edge",
			systemFound:   true,
			want:          Binary{Path: "/usr/bin/edge", Source: SourceSystem},
		},
		{
			name: "empty result preserves Rod fallback",
			want: Binary{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolveCalls := 0
			findSystemCalls := 0
			got, err := discover(
				tt.configured,
				tt.configuredSet,
				func(path string) (string, error) {
					resolveCalls++
					return tt.resolvePath, tt.resolveErr
				},
				func() (string, bool) {
					findSystemCalls++
					return tt.systemPath, tt.systemFound
				},
			)
			if (err != nil) != tt.wantErr {
				t.Fatalf("discover error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("discover = %#v, want %#v", got, tt.want)
			}
			explicit := tt.configuredSet && strings.TrimSpace(tt.configured) != ""
			if explicit && resolveCalls != 1 {
				t.Fatalf("resolve calls = %d, want 1", resolveCalls)
			}
			if explicit && findSystemCalls != 0 {
				t.Fatalf("system discovery called %d times after explicit configuration", findSystemCalls)
			}
		})
	}
}
