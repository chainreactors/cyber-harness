package task

import (
	"strings"
	"sync"
	"testing"
)

func TestOutputBufferWrite(t *testing.T) {
	buf := NewOutputBuffer(1024)
	buf.Write([]byte("hello"))
	buf.Write([]byte(" world"))
	if got := buf.String(); got != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
}

func TestOutputBufferCapOverflow(t *testing.T) {
	buf := NewOutputBuffer(10)
	buf.Write([]byte("abcdefghij")) // exactly 10
	if buf.Len() != 10 {
		t.Fatalf("Len = %d, want 10", buf.Len())
	}
	buf.Write([]byte("XYZ")) // pushes over, drops first 3
	if got := buf.String(); got != "defghijXYZ" {
		t.Fatalf("got %q, want %q", got, "defghijXYZ")
	}
	if buf.Len() != 13 {
		t.Fatalf("Len = %d, want 13", buf.Len())
	}
}

func TestOutputBufferTailLines(t *testing.T) {
	buf := NewOutputBuffer(1024)
	buf.Write([]byte("a\nb\nc\nd\ne\n"))
	got := buf.TailLines(3)
	if got != "c\nd\ne" {
		t.Fatalf("TailLines(3) = %q, want %q", got, "c\nd\ne")
	}
}

func TestOutputBufferReadSince(t *testing.T) {
	buf := NewOutputBuffer(1024)
	buf.Write([]byte("hello"))
	data, off := buf.ReadSince(0)
	if string(data) != "hello" || off != 5 {
		t.Fatalf("ReadSince(0) = (%q, %d), want (hello, 5)", data, off)
	}
	buf.Write([]byte(" world"))
	data, off = buf.ReadSince(5)
	if string(data) != " world" || off != 11 {
		t.Fatalf("ReadSince(5) = (%q, %d), want ( world, 11)", data, off)
	}
	data, off = buf.ReadSince(11)
	if data != nil || off != 11 {
		t.Fatalf("ReadSince(11) = (%v, %d), want (nil, 11)", data, off)
	}
}

func TestOutputBufferReadSinceAfterEviction(t *testing.T) {
	buf := NewOutputBuffer(10)
	buf.Write([]byte("0123456789"))
	buf.Write([]byte("ABCDE"))
	// baseOffset = 5, buf = "56789ABCDE"
	data, off := buf.ReadSince(0) // offset < baseOffset, clamps to 5
	if string(data) != "56789ABCDE" || off != 15 {
		t.Fatalf("ReadSince(0) after eviction = (%q, %d)", data, off)
	}
}

func TestOutputBufferReadSinceLimit(t *testing.T) {
	buf := NewOutputBuffer(1024)
	buf.Write([]byte("abcdefghij"))
	data, off, more, err := buf.ReadSinceLimit(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "abcde" || off != 5 || !more {
		t.Fatalf("got (%q, %d, %t), want (abcde, 5, true)", data, off, more)
	}
	data, off, more, err = buf.ReadSinceLimit(5, 5)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fghij" || off != 10 || more {
		t.Fatalf("got (%q, %d, %t), want (fghij, 10, false)", data, off, more)
	}
}

func TestOutputBufferOnWriteCallback(t *testing.T) {
	var chunks []string
	buf := NewOutputBuffer(1024)
	buf.onWrite = func(p []byte) { chunks = append(chunks, string(p)) }
	buf.Write([]byte("a"))
	buf.Write([]byte("bc"))
	if len(chunks) != 2 || chunks[0] != "a" || chunks[1] != "bc" {
		t.Fatalf("chunks = %v", chunks)
	}
}

func TestOutputBufferConcurrency(t *testing.T) {
	buf := NewOutputBuffer(1024)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				buf.Write([]byte("x"))
			}
		}()
	}
	wg.Wait()
	if buf.Len() != 1000 {
		t.Fatalf("Len = %d, want 1000", buf.Len())
	}
}

func TestOutputBufferLargeWrite(t *testing.T) {
	buf := NewOutputBuffer(10)
	buf.Write([]byte(strings.Repeat("x", 20)))
	if got := buf.String(); got != strings.Repeat("x", 10) {
		t.Fatalf("len = %d, want 10", len(got))
	}
}

func TestOutputBufferAppendError(t *testing.T) {
	buf := NewOutputBuffer(1024)
	buf.Write([]byte("output"))
	buf.AppendError("something failed")
	if !strings.Contains(buf.String(), "[task error] something failed") {
		t.Fatalf("error not appended: %q", buf.String())
	}
}
