package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
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

func TestRunPrintsVersionWithoutServer(t *testing.T) {
	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(stdout.String()), "runner v"+cfg.Version; got != want {
		t.Fatalf("version = %q, want %q", got, want)
	}
}

func TestNewApplicationRegistersRunnerTools(t *testing.T) {
	application, err := newApplication(context.Background(), new(cfg.Option), telemetry.NopLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err := application.WaitEngines(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bash", "ls"} {
		if _, ok := application.Commands.GetTool(name); !ok {
			t.Fatalf("runner tool %q is not registered", name)
		}
	}
}
