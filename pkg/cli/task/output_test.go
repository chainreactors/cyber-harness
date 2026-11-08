package task

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/events"
	cfg "github.com/chainreactors/cyber/pkg/config"
	outputpkg "github.com/chainreactors/cyber/pkg/output"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestLiveAndReplayUseTheSameFormatAndDestination(t *testing.T) {
	extension, err := anypb.New(&toolpb.Artifact{
		Tool: "gogo", Target: "127.0.0.1:80", Kind: "service", ResultId: "service",
		Data: []byte(`{"port":"80","title":"local fixture"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"csv", "markdown", "jsonl", "json"} {
		t.Run(format, func(t *testing.T) {
			var stdout bytes.Buffer
			dir := t.TempDir()
			path := filepath.Join(dir, "live.out")
			option := &cfg.Option{MiscOptions: cfg.MiscOptions{OutputFormat: format, OutputFile: path}}
			output := NewOutput(option, &stdout, io.Discard)
			if err := output.Open(); err != nil {
				t.Fatal(err)
			}
			stream := events.New()
			output.Observe(stream)
			event := &aop.Event{SessionId: "scan", TurnId: "turn", Payload: &aop.Event_Extension{Extension: extension}}
			stream.Publish(event)
			if err := output.Finish(nil); err != nil {
				t.Fatal(err)
			}
			live, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if stdout.Len() != 0 {
				t.Fatal("file output leaked to stdout")
			}
			line, err := protojson.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			history := filepath.Join(dir, "events.jsonl")
			if err := os.WriteFile(history, append(line, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			var replay bytes.Buffer
			if err := outputpkg.RenderEventFile(history, format, "", &replay); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(live, replay.Bytes()) {
				t.Fatalf("live/replay differ:\n%s\n%s", live, replay.Bytes())
			}
			if format == "csv" {
				rows, err := csv.NewReader(bytes.NewReader(live)).ReadAll()
				if err != nil || len(rows) != 2 || rows[1][0] != "127.0.0.1:80" {
					t.Fatalf("records=%v error=%v", rows, err)
				}
			}
		})
	}
}

func TestOutputNeverTruncatesHistoryOrFilesForAnInvalidFormat(t *testing.T) {
	for _, test := range []struct {
		format, input string
	}{
		{"jsonl", ""}, {"text", "resume"}, {"csv", "resume"}, {"text", "task-file"}, {"markdown", "prompt"}, {"typo", ""},
	} {
		t.Run(test.format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "history.jsonl")
			if err := os.WriteFile(path, []byte("original events"), 0600); err != nil {
				t.Fatal(err)
			}
			option := &cfg.Option{MiscOptions: cfg.MiscOptions{OutputFormat: test.format, OutputFile: path}}
			switch test.input {
			case "resume":
				option.Resume = path
			case "task-file":
				option.TaskFile = path
			case "prompt":
				option.Prompt = path
			}
			output := NewOutput(option, io.Discard, io.Discard)
			if err := output.Open(); err == nil {
				t.Fatal("unsafe destination accepted")
			}
			if data, _ := os.ReadFile(path); string(data) != "original events" {
				t.Fatal("existing history was truncated")
			}
		})
	}
}

type failedWriter struct{ error }

func (w failedWriter) Write([]byte) (int, error) { return 0, w.error }

func TestEventOutputReturnsDestinationFailures(t *testing.T) {
	for _, format := range []string{"csv", "markdown", "jsonl", "json"} {
		t.Run(format, func(t *testing.T) {
			failure := errors.New("destination unavailable")
			output := NewOutput(&cfg.Option{MiscOptions: cfg.MiscOptions{OutputFormat: format}}, failedWriter{failure}, io.Discard)
			stream := events.New()
			output.Observe(stream)
			stream.Publish(&aop.Event{SessionId: "test", Payload: &aop.Event_Status{Status: &aop.Status{State: "ready"}}})
			if err := output.Finish(nil); !errors.Is(err, failure) {
				t.Fatalf("write error lost: %v", err)
			}
		})
	}
}
