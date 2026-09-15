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
	"testing/synctest"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	filepb "github.com/chainreactors/cyber/aop/file"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/pkg/toolset"
	"github.com/chainreactors/cyber/tools/files"
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

type heldWorkspaceTool struct {
	run func(context.Context) (*tool.Result, error)
}

func (*heldWorkspaceTool) Name() string        { return "hold-skills" }
func (*heldWorkspaceTool) Description() string { return "holds a workspace call" }
func (h *heldWorkspaceTool) Definition() *tool.Definition {
	return tool.Def(h.Name(), h.Description(), struct{}{})
}
func (h *heldWorkspaceTool) Execute(ctx context.Context, _ string) (*tool.Result, error) {
	return h.run(ctx)
}

func TestWorkspaceDrainProtectsSelectedSkills(t *testing.T) {
	dir, skills := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(skills, "SKILL.md"), []byte("instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		p, err := newWorkspaceProfile(workspaceProfileConfig{
			Extensions: []string{"files", "skills"}, Files: files.Config{Directory: dir}, SkillsDirectory: skills,
		})
		if err != nil {
			t.Fatal(err)
		}
		started, release := make(chan struct{}), make(chan struct{})
		t.Cleanup(func() { _ = p.Close(context.Background()) })
		defer close(release)
		if err := p.registry.Register("test", &heldWorkspaceTool{run: func(ctx context.Context) (*tool.Result, error) {
			close(started)
			<-ctx.Done()
			<-release
			return nil, ctx.Err()
		}}); err != nil {
			t.Fatal(err)
		}
		if err := p.Load(t.Context()); err != nil {
			t.Fatal(err)
		}
		callDone := make(chan error, 1)
		go func() { _, err := p.registry.ExecuteTool(t.Context(), "hold-skills", "{}"); callDone <- err }()
		<-started
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := p.Close(ctx); !errors.Is(err, extension.ErrCloseIncomplete) {
			t.Fatalf("Close = %v", err)
		}
		if got := p.skills.Locations(); !reflect.DeepEqual(got, []string{"skill://SKILL.md"}) {
			t.Fatalf("skills released while an accepted tool call is still draining: %v", got)
		}
		// Release once; the deferred close above also covers assertion failures.
		// A send lets the call return without closing the channel twice.
		release <- struct{}{}
		if err := <-callDone; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err := p.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(p.skills.Locations()) != 0 {
			t.Fatal("retry did not close skills")
		}
	})
}
