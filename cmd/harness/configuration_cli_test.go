package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestUserConfigurationCLI(t *testing.T) {
	w := newWorkspace(t)
	path := filepath.Join(w.dir, "initialized.yaml")
	run := func(args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, executablePath, args...)
		command.Dir = w.dir
		command.Env = testEnvironment(false)
		var output, errors bytes.Buffer
		command.Stdout = &output
		command.Stderr = &errors
		if err := command.Run(); err != nil {
			t.Fatalf("config CLI failed: %v\n%s", err, errors.String())
		}
		return output.Bytes()
	}
	run("-c", path, "init", "--non-interactive", "--provider", "openai", "--model", "fixture-model", "--api-key", "fixture-private-key")
	first := readFile(t, path)
	run("-c", path, "init", "--model", "should-not-overwrite")
	if !bytes.Equal(first, readFile(t, path)) {
		t.Fatal("repeat initialization overwrote user configuration")
	}
	output := run("-c", path, "config", "show", "--sources", "--json")
	if bytes.Contains(output, []byte("fixture-private-key")) {
		t.Fatal("config CLI exposed a credential")
	}
	var view map[string]any
	if err := json.Unmarshal(output, &view); err != nil {
		t.Fatal(err)
	}
	if field(view, "config", "llm", "model") != "fixture-model" {
		t.Fatal("config CLI returned a different model")
	}
	run("-c", path, "config", "validate", "--json")
	run("-c", path, "doctor", "--json")
	if !bytes.Equal(first, readFile(t, path)) {
		t.Fatal("configuration inspection changed the file")
	}
}
