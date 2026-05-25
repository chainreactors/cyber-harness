package inbox

import (
	"fmt"
	"strings"
	"testing"
)

func TestExpandNoReferences(t *testing.T) {
	e := &Expander{}
	msg := NewUserMessage("scan 10.0.0.0/24")
	result := e.Expand(msg)
	if len(result.Attachments) != 0 {
		t.Fatalf("expected no attachments, got %d", len(result.Attachments))
	}
	if *result.ChatMessage.Content != "scan 10.0.0.0/24" {
		t.Errorf("content should be unchanged")
	}
}

func TestExpandFileReference(t *testing.T) {
	e := &Expander{
		ReadFile: func(path string) (string, error) {
			if path == "/tmp/targets.txt" {
				return "10.0.0.1\n10.0.0.2", nil
			}
			return "", fmt.Errorf("not found")
		},
	}
	msg := NewUserMessage("scan @/tmp/targets.txt")
	result := e.Expand(msg)
	if len(result.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(result.Attachments))
	}
	att := result.Attachments[0]
	if att.Type != "file" {
		t.Errorf("expected type 'file', got %q", att.Type)
	}
	if att.Content != "10.0.0.1\n10.0.0.2" {
		t.Errorf("unexpected content: %q", att.Content)
	}
	if att.Error != "" {
		t.Errorf("unexpected error: %q", att.Error)
	}
}

func TestExpandExplicitFilePrefix(t *testing.T) {
	e := &Expander{
		ReadFile: func(path string) (string, error) {
			return "data", nil
		},
	}
	msg := NewUserMessage("read @file:config.yaml")
	result := e.Expand(msg)
	if len(result.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(result.Attachments))
	}
	if result.Attachments[0].Type != "file" {
		t.Errorf("expected type 'file', got %q", result.Attachments[0].Type)
	}
}

func TestExpandSkillReference(t *testing.T) {
	e := &Expander{
		LookupSkill: func(name string) (string, bool) {
			if name == "scan" {
				return "## Scan Skill\nDo scanning", true
			}
			return "", false
		},
	}
	msg := NewUserMessage("run @skill:scan on target")
	result := e.Expand(msg)
	if len(result.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(result.Attachments))
	}
	att := result.Attachments[0]
	if att.Type != "skill" {
		t.Errorf("expected type 'skill', got %q", att.Type)
	}
	if att.Content != "## Scan Skill\nDo scanning" {
		t.Errorf("unexpected content: %q", att.Content)
	}
}

func TestExpandSkillInferredByLookup(t *testing.T) {
	e := &Expander{
		LookupSkill: func(name string) (string, bool) {
			if name == "scan" {
				return "skill body", true
			}
			return "", false
		},
	}
	msg := NewUserMessage("run @scan on target")
	result := e.Expand(msg)
	if len(result.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(result.Attachments))
	}
	if result.Attachments[0].Type != "skill" {
		t.Errorf("expected inferred skill, got %q", result.Attachments[0].Type)
	}
}

func TestExpandFileNotFound(t *testing.T) {
	e := &Expander{
		ReadFile: func(path string) (string, error) {
			return "", fmt.Errorf("no such file")
		},
	}
	msg := NewUserMessage("read @/nonexistent")
	result := e.Expand(msg)
	if len(result.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(result.Attachments))
	}
	if result.Attachments[0].Error == "" {
		t.Error("expected error in attachment")
	}
}

func TestExpandSkillNotFound(t *testing.T) {
	e := &Expander{
		LookupSkill: func(name string) (string, bool) {
			return "", false
		},
	}
	msg := NewUserMessage("run @skill:nonexistent")
	result := e.Expand(msg)
	if len(result.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(result.Attachments))
	}
	if result.Attachments[0].Error == "" {
		t.Error("expected error in attachment")
	}
}

func TestExpandFileTruncation(t *testing.T) {
	e := &Expander{
		ReadFile: func(path string) (string, error) {
			return strings.Repeat("x", 200), nil
		},
		MaxFileSize: 100,
	}
	msg := NewUserMessage("read @/tmp/big")
	result := e.Expand(msg)
	att := result.Attachments[0]
	if !strings.Contains(att.Content, "truncated") {
		t.Error("expected truncation marker")
	}
	if len(att.Content) > 200 {
		t.Error("content should be truncated")
	}
}

func TestExpandMultipleReferences(t *testing.T) {
	e := &Expander{
		ReadFile: func(path string) (string, error) {
			return "content:" + path, nil
		},
		LookupSkill: func(name string) (string, bool) {
			return "skill:" + name, true
		},
	}
	msg := NewUserMessage("scan @/tmp/a @skill:recon @/tmp/b")
	result := e.Expand(msg)
	if len(result.Attachments) != 3 {
		t.Fatalf("expected 3 attachments, got %d", len(result.Attachments))
	}
	if result.Attachments[0].Type != "file" {
		t.Errorf("att[0] expected file, got %q", result.Attachments[0].Type)
	}
	if result.Attachments[1].Type != "skill" {
		t.Errorf("att[1] expected skill, got %q", result.Attachments[1].Type)
	}
	if result.Attachments[2].Type != "file" {
		t.Errorf("att[2] expected file, got %q", result.Attachments[2].Type)
	}
}

func TestExpandNilContent(t *testing.T) {
	e := &Expander{}
	msg := Message{Origin: OriginUser}
	result := e.Expand(msg)
	if len(result.Attachments) != 0 {
		t.Error("nil content should produce no attachments")
	}
}

func TestToChatMessagesWithAttachments(t *testing.T) {
	msg := NewUserMessage("hello")
	msg.Attachments = []Attachment{
		{Type: "file", Ref: "@/tmp/a", Content: "file-data"},
		{Type: "skill", Ref: "@scan", Content: "skill-body"},
	}
	cms := msg.ToChatMessages()
	if len(cms) != 1 {
		t.Fatalf("expected 1 chat message, got %d", len(cms))
	}
	content := *cms[0].Content
	if !strings.Contains(content, "hello") {
		t.Error("should contain original content")
	}
	if !strings.Contains(content, "file-data") {
		t.Error("should contain file attachment")
	}
	if !strings.Contains(content, "skill-body") {
		t.Error("should contain skill attachment")
	}
	if !strings.Contains(content, `<attachment type="file"`) {
		t.Error("should have file attachment XML tag")
	}
}

func TestToChatMessagesWithAttachmentError(t *testing.T) {
	msg := NewUserMessage("hello")
	msg.Attachments = []Attachment{
		{Type: "file", Ref: "@/bad", Error: "not found"},
	}
	cms := msg.ToChatMessages()
	content := *cms[0].Content
	if !strings.Contains(content, "attachment_error") {
		t.Error("should contain error tag")
	}
	if !strings.Contains(content, "not found") {
		t.Error("should contain error message")
	}
}
