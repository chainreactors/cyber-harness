//go:build e2e

package e2e

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDirectScannerHelpIsNotReportedAsFailure(t *testing.T) {
	exe := buildAiscan(t)

	for _, name := range []string{"gogo", "spray", "zombie", "scan"} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(exe, name, "-h")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s -h failed: %v\n%s", name, err, out)
			}
			text := string(out)
			if strings.Contains(text, "scanner command failed") {
				t.Fatalf("%s -h reported scanner failure:\n%s", name, text)
			}
			if count := strings.Count(text, "Usage:"); count != 1 {
				t.Fatalf("%s -h Usage count = %d, want 1\n%s", name, count, text)
			}
		})
	}
}
