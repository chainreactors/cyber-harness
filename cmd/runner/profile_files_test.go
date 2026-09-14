package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/toolset"
	"github.com/chainreactors/aiscan/tools/files"
)

func TestProfileLifecycleAndActualFiles(t *testing.T) {
	dir := t.TempDir()
	p, err := newFileProfile(files.Config{Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	if p.Loaded() {
		t.Fatal("new profile is loaded")
	}
	if _, err := p.Executor(); !errors.Is(err, toolset.ErrUnavailable) {
		t.Fatalf("publication before Load: %v", err)
	}
	if err := p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !p.Loaded() {
		t.Fatal("loaded profile not reported active")
	}
	executor, err := p.Executor()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ExecuteTool(t.Context(), "write", `{"path":"note.txt","content":"cairn"}`); err != nil {
		t.Fatal(err)
	}
	result, err := executor.ExecuteTool(t.Context(), "read", `{"path":"note.txt"}`)
	if err != nil || tool.ResultText(result) != "cairn" {
		t.Fatalf("round trip: %v, %v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "note.txt"))
	if err != nil || string(data) != "cairn" {
		t.Fatalf("disk result: %q, %v", data, err)
	}
	if err := p.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if p.Loaded() {
		t.Fatal("closed profile reported loaded")
	}
	if _, err := executor.ExecuteTool(t.Context(), "read", `{"path":"note.txt"}`); !errors.Is(err, toolset.ErrUnavailable) {
		t.Fatalf("execution after Close: %v", err)
	}
	if _, err := p.Executor(); !errors.Is(err, toolset.ErrUnavailable) {
		t.Fatalf("publication after Close: %v", err)
	}
	if err := p.Load(t.Context()); err == nil {
		t.Fatal("closed profile reloaded")
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestReadOnlyProfile(t *testing.T) {
	p, err := newFileProfile(files.Config{Directory: t.TempDir(), ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	if err := p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	executor, err := p.Executor()
	if err != nil {
		t.Fatal(err)
	}
	defs := executor.ToolDefinitions()
	if len(defs) != 3 || defs[0].Name != "read" {
		t.Fatalf("definitions: %v", defs)
	}
}

func TestFailedLoadDoesNotPublishExecutor(t *testing.T) {
	p, err := newFileProfile(files.Config{Directory: filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Load(t.Context()); err == nil {
		t.Fatal("loaded missing root")
	}
	if _, err := p.Executor(); !errors.Is(err, toolset.ErrUnavailable) {
		t.Fatalf("partial profile published executor: %v", err)
	}
	if p.Loaded() {
		t.Fatal("failed profile reported active")
	}
	if err := p.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentCloseNeverRepublishesProfile(t *testing.T) {
	p, err := newFileProfile(files.Config{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() { _ = p.Load(t.Context()) })
		wg.Go(func() {
			if err := p.Close(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if p.Loaded() {
		t.Fatal("closing profile reported active")
	}
	if _, err := p.Executor(); !errors.Is(err, toolset.ErrUnavailable) {
		t.Fatalf("closed profile republished: %v", err)
	}
}
