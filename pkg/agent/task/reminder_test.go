package task

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPeekSinceIncremental(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	mgr := NewManager()

	fn := func(ctx context.Context, out io.Writer) error {
		fmt.Fprintln(out, "line1")
		fmt.Fprintln(out, "line2")
		time.Sleep(200 * time.Millisecond)
		fmt.Fprintln(out, "line3")
		fmt.Fprintln(out, "line4")
		return nil
	}
	info, err := mgr.SpawnInProcess("peek-since", "peek-since-cmd", 10*time.Second, fn)
	if err != nil {
		t.Fatalf("SpawnInProcess: %v", err)
	}

	waitUntil(t, 2*time.Second, func() bool {
		out, _ := mgr.Peek(info.ID, 30)
		return strings.Contains(out, "line2")
	})

	out1, off1, err := mgr.PeekSince(info.ID, 0)
	if err != nil {
		t.Fatalf("PeekSince(0): %v", err)
	}
	if !strings.Contains(out1, "line1") || !strings.Contains(out1, "line2") {
		t.Fatalf("PeekSince(0) = %q, want line1+line2", out1)
	}
	if off1 == 0 {
		t.Fatal("offset should advance past 0")
	}

	waitUntil(t, 2*time.Second, func() bool {
		final, _ := mgr.Get(info.ID)
		return final.State == StateCompleted
	})

	out2, off2, err := mgr.PeekSince(info.ID, off1)
	if err != nil {
		t.Fatalf("PeekSince(%d): %v", off1, err)
	}
	if strings.Contains(out2, "line1") || strings.Contains(out2, "line2") {
		t.Fatalf("PeekSince(%d) should not contain old lines: %q", off1, out2)
	}
	if !strings.Contains(out2, "line3") || !strings.Contains(out2, "line4") {
		t.Fatalf("PeekSince(%d) = %q, want line3+line4", off1, out2)
	}
	if off2 <= off1 {
		t.Fatalf("offset did not advance: %d <= %d", off2, off1)
	}

	out3, off3, err := mgr.PeekSince(info.ID, off2)
	if err != nil {
		t.Fatalf("PeekSince(%d): %v", off2, err)
	}
	if out3 != "" {
		t.Fatalf("PeekSince(%d) = %q, want empty", off2, out3)
	}
	if off3 != off2 {
		t.Fatalf("offset changed on empty read: %d != %d", off3, off2)
	}
}

func TestPeekSinceLimitPagesWithoutSkipping(t *testing.T) {
	mgr := NewManager()

	payload := strings.Repeat("a", 12)
	fn := func(ctx context.Context, out io.Writer) error {
		_, _ = io.WriteString(out, payload)
		return nil
	}
	info, err := mgr.SpawnInProcess("peek-limit", "peek-limit-cmd", 10*time.Second, fn)
	if err != nil {
		t.Fatalf("SpawnInProcess: %v", err)
	}
	final, err := mgr.Wait(context.Background(), info.ID, 3*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if final.State != StateCompleted {
		t.Fatalf("state = %s, want completed", final.State)
	}

	out1, off1, more1, err := mgr.PeekSinceLimit(info.ID, 0, 5)
	if err != nil {
		t.Fatalf("PeekSinceLimit first: %v", err)
	}
	if out1 != strings.Repeat("a", 5) || off1 != 5 || !more1 {
		t.Fatalf("first page = (%q, %d, %t), want 5 bytes, offset 5, more", out1, off1, more1)
	}

	out2, off2, more2, err := mgr.PeekSinceLimit(info.ID, off1, 5)
	if err != nil {
		t.Fatalf("PeekSinceLimit second: %v", err)
	}
	if out2 != strings.Repeat("a", 5) || off2 != 10 || !more2 {
		t.Fatalf("second page = (%q, %d, %t), want 5 bytes, offset 10, more", out2, off2, more2)
	}

	out3, off3, more3, err := mgr.PeekSinceLimit(info.ID, off2, 5)
	if err != nil {
		t.Fatalf("PeekSinceLimit third: %v", err)
	}
	if out3 != strings.Repeat("a", 2) || off3 != 12 || more3 {
		t.Fatalf("third page = (%q, %d, %t), want 2 bytes, offset 12, no more", out3, off3, more3)
	}
}

func TestPeekSinceUnknownTask(t *testing.T) {
	mgr := NewManager()

	_, _, err := mgr.PeekSince("nonexistent", 0)
	if err == nil {
		t.Fatal("expected error for unknown task")
	}
}
