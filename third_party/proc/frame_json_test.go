package proc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestFrameJSONRoundTrip(t *testing.T) {
	want := Frame{
		Type:      FrameOutput,
		StreamID:  "terminal-1",
		SessionID: "session-1",
		Data:      []byte{0xff, 0x00, 'x'},
		Offset:    42,
		Session: &Info{
			ID:          "session-1",
			State:       StateRunning,
			ActivitySeq: 3,
			OutputBytes: 99,
		},
	}

	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	for _, field := range []string{`"type":"output"`, `"stream_id":"terminal-1"`, `"session_id":"session-1"`, `"data":"/wB4"`, `"offset":42`} {
		if !strings.Contains(encoded, field) {
			t.Fatalf("frame JSON %s does not contain %s", encoded, field)
		}
	}

	var got Frame
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != want.Type || got.StreamID != want.StreamID || got.SessionID != want.SessionID || got.Offset != want.Offset || !bytes.Equal(got.Data, want.Data) {
		t.Fatalf("round trip frame = %+v", got)
	}
	if got.Session == nil || got.Session.ActivitySeq != 3 || got.Session.OutputBytes != 99 {
		t.Fatalf("round trip session = %+v", got.Session)
	}
}

func TestManagerTracksSessionActivity(t *testing.T) {
	mgr := NewManager()
	release := make(chan struct{})
	info, err := mgr.Start(context.Background(), Spec{Name: "activity", Timeout: time.Second},
		Func(func(_ context.Context, w io.Writer) error {
			_, _ = w.Write([]byte("hello"))
			<-release
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}

	waitUntil(t, time.Second, func() bool {
		current, ok := mgr.Get(info.ID)
		return ok && current.ActivitySeq >= 2 && current.OutputBytes == 5 && !current.LastActivityAt.IsZero()
	})
	close(release)
	final, err := mgr.Wait(context.Background(), info.ID, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != StateCompleted || final.ActivitySeq < 3 || final.LastActivityAt.Before(final.StartedAt) {
		t.Fatalf("final activity = %+v", final)
	}
}
