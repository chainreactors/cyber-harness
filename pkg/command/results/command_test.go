package results

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseResultsAcceptsPositionalScannerAndFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "gogo.jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := (&ParseResultsCommand{}).Execute(context.Background(), []string{"gogo", "--file", file, "--analysis", "summary"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out, "Total results: 1") {
		t.Fatalf("output = %q, want parsed summary", out)
	}
}

func TestFilterResultsAcceptsKeyValueFilterAndFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "gogo.jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := (&FilterResultsCommand{}).Execute(context.Background(), []string{"gogo", "--file", file, "--filter", "port=80,protocol=http"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out, "Matched") {
		t.Fatalf("output = %q, want filter summary", out)
	}
}
