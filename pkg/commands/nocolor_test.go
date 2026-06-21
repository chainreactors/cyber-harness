package commands

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type argsCapture struct {
	name string
	got  []string
}

func (c *argsCapture) Name() string  { return c.name }
func (c *argsCapture) Usage() string { return c.name }
func (c *argsCapture) Execute(_ context.Context, args []string) error {
	c.got = append([]string(nil), args...)
	fmt.Fprint(Output, strings.Join(args, " "))
	return nil
}

func TestNormalizeNoColorInjectForScan(t *testing.T) {
	reg := NewRegistry()
	cmd := &argsCapture{name: "scan"}
	reg.Register(cmd, "")

	_, err := reg.ExecuteArgs(context.Background(), []string{"scan", "-i", "10.0.0.1"})
	if err != nil {
		t.Fatalf("ExecuteArgs error: %v", err)
	}
	for _, a := range cmd.got {
		if a == "--no-color" {
			return
		}
	}
	t.Fatalf("scan should get --no-color auto-injected, got %v", cmd.got)
}

func TestNormalizeNoColorScanNoDuplicate(t *testing.T) {
	reg := NewRegistry()
	cmd := &argsCapture{name: "scan"}
	reg.Register(cmd, "")

	_, err := reg.ExecuteArgs(context.Background(), []string{"scan", "-i", "10.0.0.1", "--no-color"})
	if err != nil {
		t.Fatalf("ExecuteArgs error: %v", err)
	}
	count := 0
	for _, a := range cmd.got {
		if a == "--no-color" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("--no-color should appear exactly once, got %d in %v", count, cmd.got)
	}
}

func TestNormalizeNoColorSkipsNonScan(t *testing.T) {
	reg := NewRegistry()
	cmd := &argsCapture{name: "gogo"}
	reg.Register(cmd, "")

	_, err := reg.ExecuteArgs(context.Background(), []string{"gogo", "-i", "10.0.0.1"})
	if err != nil {
		t.Fatalf("ExecuteArgs error: %v", err)
	}
	for _, a := range cmd.got {
		if a == "--no-color" {
			t.Fatalf("gogo should not get --no-color, got %v", cmd.got)
		}
	}
}
