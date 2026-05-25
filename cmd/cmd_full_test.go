//go:build full

package cmd

import (
	"reflect"
	"testing"
)

func TestParseCLIReconCommandsAndFlags(t *testing.T) {
	parsed, err := parseCLI([]string{
		"--fofa-email", "ops@example.com",
		"--fofa-key", "FOFAKEY",
		"--hunter-api-key", "HUNTERKEY",
		"--recon-proxy", "socks5://127.0.0.1:1080",
		"--recon-limit", "0",
		"passive",
		`domain="example.com"`,
		"-s", "fofa",
	})
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Mode != runModeScanner {
		t.Fatalf("mode = %s, want %s", parsed.Mode, runModeScanner)
	}
	wantArgs := []string{"passive", `domain="example.com"`, "-s", "fofa"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
	if parsed.Option.FofaEmail != "ops@example.com" || parsed.Option.FofaKey != "FOFAKEY" || parsed.Option.HunterAPIKey != "HUNTERKEY" || parsed.Option.ReconProxy != "socks5://127.0.0.1:1080" {
		t.Fatalf("recon options = %#v", parsed.Option.ReconOptions)
	}
	if parsed.Option.ReconLimit == nil || *parsed.Option.ReconLimit != 0 {
		t.Fatalf("recon limit = %#v, want explicit 0", parsed.Option.ReconLimit)
	}
}
