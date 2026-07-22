package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePromptLoadsExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task.md")
	if err := os.WriteFile(path, []byte("\n  inspect the exposed services  \n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ResolvePrompt(path)
	if err != nil {
		t.Fatalf("ResolvePrompt() error = %v", err)
	}
	if got != "inspect the exposed services" {
		t.Fatalf("ResolvePrompt() = %q", got)
	}
}

func TestResolvePromptKeepsMissingPathAsNaturalLanguage(t *testing.T) {
	prompt := filepath.Join(t.TempDir(), "missing-task.md")

	got, err := ResolvePrompt(prompt)
	if err != nil {
		t.Fatalf("ResolvePrompt() error = %v", err)
	}
	if got != prompt {
		t.Fatalf("ResolvePrompt() = %q, want %q", got, prompt)
	}
}

func TestResolveTaskLoadsPromptFileAndAppendsInputs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task.md")
	if err := os.WriteFile(path, []byte("inspect the exposed services"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveTask(&Option{AgentOptions: AgentOptions{
		Prompt: path,
		Inputs: []string{"https://example.com"},
	}})
	if err != nil {
		t.Fatalf("ResolveTask() error = %v", err)
	}
	want := "inspect the exposed services\n\nTargets:\n- https://example.com"
	if got != want {
		t.Fatalf("ResolveTask() = %q, want %q", got, want)
	}
}
