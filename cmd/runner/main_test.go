package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
	filesprofile "github.com/chainreactors/aiscan/pkg/profile/files"
	"github.com/chainreactors/aiscan/tools/files"
)

func TestParseOptionsRequiresServer(t *testing.T) {
	if _, err := parseOptions(nil, io.Discard); err == nil {
		t.Fatal("missing server must be rejected")
	}
	options, err := parseOptions([]string{"--server", "http://127.0.0.1:8080"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if options.server != "http://127.0.0.1:8080" {
		t.Fatalf("server = %q", options.server)
	}
}

func TestDiscoverSelectedExtensionsWithoutServer(t *testing.T) {
	var stdout bytes.Buffer
	if err := run(t.Context(), []string{"--discover", "--workdir", t.TempDir(), "--read-only"}, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	var result struct{ Installed, Tools []string }
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Installed) != 1 || result.Installed[0] != "files" || len(result.Tools) != 3 {
		t.Fatalf("discovery: %s", stdout.String())
	}
}

func TestRunnerRejectsUnselectedExtensionOptions(t *testing.T) {
	if err := run(t.Context(), []string{"--discover", "--workdir", t.TempDir(), "--skills-dir", t.TempDir()}, io.Discard, io.Discard); err == nil {
		t.Fatal("ignored unselected skills configuration")
	}
}

func TestRunPrintsVersionWithoutServer(t *testing.T) {
	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(stdout.String()), "runner v"+cfg.Version; got != want {
		t.Fatalf("version = %q, want %q", got, want)
	}
}

func TestFilesProfileRegistersFileTools(t *testing.T) {
	profile, err := filesprofile.New(files.Config{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer profile.Close(context.Background())
	if err := profile.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	executor, err := profile.Executor()
	if err != nil {
		t.Fatal(err)
	}
	definitions := executor.ToolDefinitions()
	if len(definitions) != 4 || definitions[0].Name != "read" || definitions[3].Name != "write" {
		t.Fatalf("runner tools = %+v", definitions)
	}
}
