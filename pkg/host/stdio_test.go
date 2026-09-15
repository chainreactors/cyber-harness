package host

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	aop "github.com/chainreactors/cyber/aop"
)

type shortWriter struct{ writes int }

func (w *shortWriter) Write(p []byte) (int, error) {
	w.writes++
	return len(p) - 1, nil
}

func TestHostRetainsStdioShortWriteFailure(t *testing.T) {
	w := new(shortWriter)
	stream := NewStdio(strings.NewReader(""), w)
	h := testHost(t, aop.NewNamespaceMux(t.Context()))
	for i := 0; i < 2; i++ {
		if err := h.Send(aop.Reply("request", aop.NewProtocolError("EXAMPLE", "message")), stream.Send); !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("send error = %v", err)
		}
	}
	if !errors.Is(h.Err(), io.ErrShortWrite) || w.writes != 1 {
		t.Fatalf("sticky error=%v writes=%d", h.Err(), w.writes)
	}
}

func TestStdioConcurrentWritesKeepFramesIntact(t *testing.T) {
	var output bytes.Buffer
	stream := NewStdio(strings.NewReader(""), &output)
	h := testHost(t, aop.NewNamespaceMux(t.Context()))
	var pending sync.WaitGroup
	for i := 0; i < 30; i++ {
		pending.Add(1)
		go func() {
			defer pending.Done()
			_ = h.Send(aop.Reply("request", aop.NewProtocolError("EXAMPLE", "message")), stream.Send)
		}()
	}
	pending.Wait()
	if err := h.Err(); err != nil {
		t.Fatal(err)
	}
	reader := NewStdio(&output, io.Discard)
	for i := 0; i < 30; i++ {
		if _, err := reader.Recv(); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	if _, err := reader.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("end of stream: %v", err)
	}
}

func TestStdioRejectsMalformedInputAfterBlankLines(t *testing.T) {
	stream := NewStdio(strings.NewReader("\n \r\nnot json\n"), io.Discard)
	if _, err := stream.Recv(); err == nil || !strings.Contains(err.Error(), "decode stdio envelope") {
		t.Fatalf("decode error: %v", err)
	}
}
