package tui

import (
	"reflect"
	"testing"
)

func TestAgentConsoleArgsForLineBangCommand(t *testing.T) {
	got, err := AgentConsoleArgsForLine("!echo chat_pass")
	if err != nil {
		t.Fatalf("AgentConsoleArgsForLine returned error: %v", err)
	}
	want := []string{"!", "echo chat_pass"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AgentConsoleArgsForLine = %#v, want %#v", got, want)
	}
}
