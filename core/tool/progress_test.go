package tool

import (
	"github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/eventbus"
	"testing"
	"unicode/utf8"
)

func TestProgressStreamerSanitizesInvalidUTF8(t *testing.T) {
	progressBus := eventbus.New[*toolpb.Progress]()
	var progress []*toolpb.Progress
	progressBus.Subscribe(func(event *toolpb.Progress) {
		progress = append(progress, event)
	})
	stream := progressStreamer{bus: progressBus, tool: "bash", callID: "task-invalid"}
	stream.Write([]byte{'o', 'k', 0xff, '\n'})
	stream.Write([]byte{0xe4, 0xbd})
	stream.Write([]byte{0xa0, '\n'})
	stream.Flush()

	if len(progress) != 2 {
		t.Fatalf("progress count = %d", len(progress))
	}
	if progress[0].Text != "ok\uFFFD" || progress[1].Text != "\u4f60" {
		t.Fatalf("progress = %#v", progress)
	}
	for _, item := range progress {
		if !utf8.ValidString(item.Text) {
			t.Fatalf("progress is not valid UTF-8: %q", item.Text)
		}
		message := &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Progress{Progress: item}}
		if _, err := aop.Wrap("progress", item.CallId, message); err != nil {
			t.Fatalf("wrap progress: %v", err)
		}
	}
}
