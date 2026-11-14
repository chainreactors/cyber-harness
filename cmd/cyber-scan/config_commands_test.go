package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/pkg/cli/configuration"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func TestDoctorRejectsInvalidScannerConfiguration(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "invalid.yaml")
	if err := os.WriteFile(path, []byte("extensions:\n  scan:\n    verify: invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	host := configuration.Host{
		Name: "cyber-scan", Sections: defaultSections(), Checks: configChecks,
		Context: &cfg.Context{
			Directory: directory, Home: directory,
			LookupEnv: func(string) (string, bool) { return "", false },
		},
		Out: io.Discard, Err: io.Discard,
	}
	handled, err := configuration.Run(t.Context(), []string{"doctor", "--online", "--config", path}, host)
	if !handled || err == nil || !strings.Contains(err.Error(), "verify") {
		t.Fatalf("invalid scanner configuration was accepted: handled=%v err=%v", handled, err)
	}
}
