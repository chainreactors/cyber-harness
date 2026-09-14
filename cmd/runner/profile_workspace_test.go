package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	filepb "github.com/chainreactors/aiscan/aop/file"
	operationpb "github.com/chainreactors/aiscan/aop/operation"
	"github.com/chainreactors/aiscan/pkg/toolset"
	"github.com/chainreactors/aiscan/tools/files"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestSelectedExtensionsOperateAndDrainThroughProfile(t *testing.T) {
	dir, skills, logs := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(skills, "SKILL.md"), []byte("workspace instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logs, "events.jsonl")
	p, err := newWorkspaceProfile(workspaceProfileConfig{Extensions: []string{"skills", "observe", "files"}, Files: files.Config{Directory: dir}, Output: path, SkillsDirectory: skills})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("construction opened event output: %v", err)
	}
	if _, err := p.Executor(); !errors.Is(err, toolset.ErrUnavailable) {
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
	if _, err := executor.ExecuteTool(t.Context(), "read", `{"path":"note"}`); !errors.Is(err, toolset.ErrUnavailable) {
		t.Fatalf("retained executor admitted: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var records []*filepb.Access
	var refs []*operationpb.Ref
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		event := new(aop.Event)
		if err := protojson.Unmarshal(scanner.Bytes(), event); err != nil {
			t.Fatal(err)
		}
		record := new(filepb.Access)
		if event.GetExtension() == nil || !event.GetExtension().MessageIs(record) {
			continue
		}
		if err := event.GetExtension().UnmarshalTo(record); err != nil {
			t.Fatal(err)
		}
		ref := new(operationpb.Ref)
		if ok, err := aop.FindTypedExtension(event, ref); err != nil || !ok {
			t.Fatalf("file event has no operation: %v %v", event, err)
		}
		records, refs = append(records, record), append(refs, ref)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("edited"))
	if len(records) != 2 || records[0].Op != filepb.AccessOp_ACCESS_OP_CREATE || records[1].Op != filepb.AccessOp_ACCESS_OP_EDIT || records[1].Edits != 1 || records[1].Digest != hex.EncodeToString(digest[:]) || refs[0].GetOperationId() == "" || refs[1].GetOperationId() == "" {
		t.Fatalf("event output did not drain canonical committed edits: %v", records)
	}
}

func TestSelectionRejectsInvalidConfigurationWithoutSideEffects(t *testing.T) {
	for _, ids := range [][]string{{"unknown"}, {"files", "files"}, {"skills"}, {}, {"files", "skills"}} {
		p, err := newWorkspaceProfile(workspaceProfileConfig{Extensions: ids, Files: files.Config{Directory: t.TempDir()}})
		if err == nil {
			p.Close(context.Background())
			t.Fatalf("accepted invalid selection: %v", ids)
		}
	}
	root := t.TempDir()
	path := filepath.Join(root, "not-created", "events.jsonl")
	if profile, err := newWorkspaceProfile(workspaceProfileConfig{Files: files.Config{Directory: root}, Output: path}); err != nil {
		t.Fatalf("output should be independently selectable: %v", err)
	} else {
		_ = profile.Close(context.Background())
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid configuration touched disk: %v", err)
	}
}

func TestProfilesHaveIndependentSelectionAndFailureCleanup(t *testing.T) {
	p, err := newWorkspaceProfile(workspaceProfileConfig{Files: files.Config{Directory: t.TempDir(), ReadOnly: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	if err := p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	bad, err := newWorkspaceProfile(workspaceProfileConfig{Extensions: []string{"files", "skills"}, Files: files.Config{Directory: t.TempDir()}, SkillsDirectory: filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if err := bad.Load(t.Context()); err == nil {
		t.Fatal("loaded missing mount")
	}
	if err := bad.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Executor(); !errors.Is(err, toolset.ErrUnavailable) {
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
