package workspace_test

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	filepb "github.com/chainreactors/aiscan/aop/file"
	"github.com/chainreactors/aiscan/pkg/fileaudit"
	"github.com/chainreactors/aiscan/pkg/files"
	"github.com/chainreactors/aiscan/pkg/profile/workspace"
	"github.com/chainreactors/aiscan/pkg/toolset/registry"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestSelectedExtensionsOperateAndDrainThroughProfile(t *testing.T) {
	dir, skills, logs := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(skills, "SKILL.md"), []byte("workspace instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logs, "audit.jsonl")
	p, err := workspace.New(workspace.Config{Extensions: []string{"skills", "file-audit", "files"}, Files: files.Config{Directory: dir}, AuditLog: path, SkillsDirectory: skills})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("construction opened journal: %v", err)
	}
	if _, err := p.Executor(); !errors.Is(err, registry.ErrUnavailable) {
		t.Fatalf("published before Load: %v", err)
	}
	if len(p.Installed()) != 0 {
		t.Fatal("reported unloaded extensions")
	}
	if err := p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	executor, err := p.Executor()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ExecuteTool(t.Context(), "write", `{"path":"note","content":"original"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ExecuteTool(t.Context(), "write", `{"path":"note","edits":[{"old_text":"original","new_text":"edited"}]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ExecuteTool(t.Context(), "read", `{"path":"skill://SKILL.md"}`); err != nil {
		t.Fatal(err)
	}
	if got := p.SkillLocations(); !reflect.DeepEqual(got, []string{"skill://SKILL.md"}) {
		t.Fatalf("skills: %v", got)
	}
	if err := p.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(p.Installed()) != 0 {
		t.Fatal("reported closed extensions")
	}
	if _, err := executor.ExecuteTool(t.Context(), "read", `{"path":"note"}`); !errors.Is(err, registry.ErrUnavailable) {
		t.Fatalf("retained executor admitted: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var records []*filepb.Access
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		record := new(filepb.Access)
		if err := protojson.Unmarshal(scanner.Bytes(), record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Op != filepb.AccessOp_ACCESS_OP_CREATE || records[1].Op != filepb.AccessOp_ACCESS_OP_EDIT || records[1].Edits != 1 || records[1].Digest != fileaudit.Digest([]byte("edited")) {
		t.Fatalf("audit did not drain canonical committed edits: %v", records)
	}
}

func TestSelectionRejectsInvalidConfigurationWithoutSideEffects(t *testing.T) {
	for _, ids := range [][]string{{"unknown"}, {"files", "files"}, {"skills"}, {}, {"files", "file-audit"}, {"files", "skills"}} {
		p, err := workspace.New(workspace.Config{Extensions: ids, Files: files.Config{Directory: t.TempDir()}})
		if err == nil {
			p.Close(context.Background())
			t.Fatalf("accepted invalid selection: %v", ids)
		}
	}
	root := t.TempDir()
	path := filepath.Join(root, "not-created", "audit.jsonl")
	if _, err := workspace.New(workspace.Config{Files: files.Config{Directory: root}, AuditLog: path}); err == nil {
		t.Fatal("silently ignored unselected audit configuration")
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid configuration touched disk: %v", err)
	}
}

func TestProfilesHaveIndependentSelectionAndFailureCleanup(t *testing.T) {
	p, err := workspace.New(workspace.Config{Files: files.Config{Directory: t.TempDir(), ReadOnly: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	if err := p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	bad, err := workspace.New(workspace.Config{Extensions: []string{"files", "skills"}, Files: files.Config{Directory: t.TempDir()}, SkillsDirectory: filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if err := bad.Load(t.Context()); err == nil {
		t.Fatal("loaded missing mount")
	}
	if err := bad.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Executor(); !errors.Is(err, registry.ErrUnavailable) {
		t.Fatalf("failed profile published: %v", err)
	}
	if got := p.Installed(); !reflect.DeepEqual(got, []string{"files"}) {
		t.Fatalf("other profile selection changed: %v", got)
	}
	executor, err := p.Executor()
	if err != nil {
		t.Fatal(err)
	}
	if len(executor.ToolDefinitions()) != 3 {
		t.Fatal("other profile registration changed")
	}
}
