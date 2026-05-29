package task

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPeekNewIncremental(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	mgr := NewManager()
	dir := t.TempDir()

	info, err := mgr.Create(dir, "echo line1; echo line2; sleep 0.2; echo line3; echo line4", "peek-inc", 10*time.Second, nil, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	waitUntil(t, 2*time.Second, func() bool {
		out, _ := mgr.Peek(info.ID, 30)
		return strings.Contains(out, "line2")
	})

	out1, _, err := mgr.PeekNew(info.ID, 0)
	if err != nil {
		t.Fatalf("PeekNew first: %v", err)
	}
	if !strings.Contains(out1, "line1") || !strings.Contains(out1, "line2") {
		t.Fatalf("first PeekNew = %q, want line1+line2", out1)
	}

	<-mgr.Done(info.ID)

	out2, _, err := mgr.PeekNew(info.ID, 0)
	if err != nil {
		t.Fatalf("PeekNew second: %v", err)
	}
	if strings.Contains(out2, "line1") || strings.Contains(out2, "line2") {
		t.Fatalf("second PeekNew should not contain old lines: %q", out2)
	}
	if !strings.Contains(out2, "line3") || !strings.Contains(out2, "line4") {
		t.Fatalf("second PeekNew = %q, want line3+line4", out2)
	}

	out3, _, err := mgr.PeekNew(info.ID, 0)
	if err != nil {
		t.Fatalf("PeekNew third: %v", err)
	}
	if out3 != "" {
		t.Fatalf("third PeekNew = %q, want empty", out3)
	}
}

func TestPeekNewUnknownSession(t *testing.T) {
	mgr := NewManager()
	_, _, err := mgr.PeekNew("nonexistent", 0)
	if err == nil {
		t.Fatal("expected error for unknown session")
	}
}
