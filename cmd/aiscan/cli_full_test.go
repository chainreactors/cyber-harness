//go:build full

package main

import (
	"io"
	"reflect"
	"testing"

	passiveconfig "github.com/chainreactors/cyber/exts/passive/config"

	cfg "github.com/chainreactors/cyber/pkg/config"
)

func TestParseCLIReconCommandsAndFlags(t *testing.T) {
	parsed, err := parseCLI([]string{
		"--fofa-key", "FOFAKEY",
		"--hunter-api-key", "HUNTERKEY",
		"--recon-proxy", "socks5://127.0.0.1:1080",
		"--recon-limit", "0",
		"passive",
		`domain="example.com"`,
		"-s", "fofa",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseCLI() error = %v", err)
	}
	if parsed.Mode != cfg.RunModeScanner {
		t.Fatalf("mode = %s, want %s", parsed.Mode, cfg.RunModeScanner)
	}
	wantArgs := []string{"passive", `domain="example.com"`, "-s", "fofa"}
	if !reflect.DeepEqual(parsed.ScannerArgs, wantArgs) {
		t.Fatalf("scanner args = %#v, want %#v", parsed.ScannerArgs, wantArgs)
	}
	recon, err := passiveconfig.ReadRecon(&parsed.Option)
	if err != nil {
		t.Fatal(err)
	}
	if recon.FofaKey != "FOFAKEY" || recon.HunterAPIKey != "HUNTERKEY" || recon.Proxy != "socks5://127.0.0.1:1080" {
		t.Fatalf("recon options = %#v", recon)
	}
	if recon.Limit == nil || *recon.Limit != 0 {
		t.Fatalf("recon limit = %#v, want explicit 0", recon.Limit)
	}
}
