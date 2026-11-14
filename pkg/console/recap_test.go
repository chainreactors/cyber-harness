package console

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/truncate"
	"github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/output"
	rlterm "github.com/chainreactors/tui/readline/terminal"
	"google.golang.org/protobuf/types/known/anypb"
)

func recapEvent(turn, text string) *aop.Event {
	value, _ := anypb.New(&types.Recap{Text: text})
	return &aop.Event{TurnId: turn, Payload: &aop.Event_Extension{Extension: value}}
}

func TestRecapAppearsOnceAfterAnswerAndPreservesTurn(t *testing.T) {
	for _, early := range []bool{false, true} {
		var stderr bytes.Buffer
		o := testOutput(&stderr, 1, false)
		o.mode = ModeInteractive
		o.HandleEvent(turnStartEvent(1))
		o.stream.stdout = &stderr
		o.HandleEvent(messageEvent("answer", "assistant", aop.Text("Final answer.")))
		other := recapEvent("run-test", "WRONG_SESSION")
		other.SessionId = "other"
		o.HandleEvent(other)
		if o.recapText != "" {
			t.Fatal("recap crossed sessions")
		}
		event := recapEvent("run-test", "Checked the implementation.")
		if early {
			o.HandleEvent(event)
			if strings.Contains(stderr.String(), "Checked the implementation.") {
				t.Fatal("recap displayed before task ended")
			}
		}
		o.HandleEvent(turnEndEvent(1, 0))
		o.HandleEvent(event)
		o.HandleEvent(event)
		if strings.Count(stderr.String(), "Checked the implementation.") != 1 {
			t.Fatalf("recap not displayed exactly once: %q", stderr.String())
		}
		if answer, recap := strings.Index(stderr.String(), "Final answer."), strings.Index(stderr.String(), "✻ Checked the implementation."); answer < 0 || recap <= answer {
			t.Fatalf("recap did not follow final answer: %q", stderr.String())
		}
		next := turnStartEvent(2)
		next.TurnId = "next"
		o.HandleEvent(next)
		o.HandleEvent(recapEvent("run-test", "LATE_OLD_RECAP"))
		if strings.Contains(stderr.String(), "LATE_OLD_RECAP") {
			t.Fatal("old recap entered next task")
		}
	}
}

func TestRecapFreezesElapsedAndRendersOnePlainLine(t *testing.T) {
	var stderr bytes.Buffer
	o := testOutput(&stderr, 1, false)
	o.HandleEvent(turnStartEvent(1))
	o.agentStart = time.Now().Add(-2 * time.Second)
	o.HandleEvent(turnEndEvent(1, 0))
	elapsed := o.recapElapsed
	if elapsed < 2*time.Second || elapsed >= 3*time.Second {
		t.Fatalf("elapsed = %s", elapsed)
	}
	// A late annotation must not include its own generation time.
	o.agentStart = time.Now().Add(-time.Hour)
	o.HandleEvent(turnEndEvent(1, 0))
	stderr.Reset()
	o.HandleEvent(recapEvent("run-test", "\x1b[31m已检查\x1b[0m\r\n本地\t结果。\a"))
	want := "\n  ✻ 已检查 本地 结果。 · " + truncate.FormatDuration(elapsed) + "\n\n"
	if got := stderr.String(); got != want {
		t.Fatalf("recap = %q, want %q", got, want)
	}
}

func TestRecapFollowsFinalReplyAfterToolIteration(t *testing.T) {
	var terminal bytes.Buffer
	o := testOutput(&terminal, 1, false)
	o.stream.stdout = &terminal
	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(textDeltaEvent("progress", "Checking the implementation with a local tool."))
	o.HandleEvent(messageEvent("progress", "assistant", aop.Text("Checking the implementation with a local tool.")))
	o.HandleEvent(toolCallEvent("read-1", "read", `{}`))
	o.HandleEvent(toolResultEvent("read-1", "read", "ok", false))
	o.HandleEvent(textDeltaEvent("final", "Done."))
	o.HandleEvent(messageEvent("final", "assistant", aop.Text("Done.")))
	o.HandleEvent(turnEndEvent(1, 0))
	o.HandleEvent(recapEvent("run-test", "Checked the implementation."))
	text := terminal.String()
	if strings.Count(text, "Done.") != 1 || strings.Index(text, "Done.") >= strings.Index(text, "✻ Checked the implementation.") {
		t.Fatalf("final reply missing, duplicated or out of order: %q", text)
	}
}

func TestRecapEmptyAnnotationDoesNotPrint(t *testing.T) {
	var stderr bytes.Buffer
	o := testOutput(&stderr, 1, false)
	o.HandleEvent(turnStartEvent(1))
	o.HandleEvent(turnEndEvent(1, 0))
	stderr.Reset()
	o.HandleEvent(recapEvent("run-test", "\x1b[31m\a\r\n\x1b[0m"))
	if got := stderr.String(); got != "" {
		t.Fatalf("empty recap = %q", got)
	}
}

func TestRecapPreservesReadlineDraftAndCursor(t *testing.T) {
	for _, draft := range []string{"继续检查 next task", "first line\n继续检查 next task"} {
		bridge, raw := testReadlineBridge(t)
		o := testOutput(raw, 1, false)
		o.SetReadlineMode(bridge)
		o.HandleEvent(turnStartEvent(1))
		o.HandleEvent(turnEndEvent(1, 0))
		shell := bridge.shell
		shell.Prompt.Primary(func() string { return "cyber-scan> " })
		shell.Line().Set([]rune(draft)...)
		shell.Cursor().Set(6)
		shell.RefreshPrimaryWithoutAutocomplete()
		bridge.SetReady(true)
		raw.Reset()
		o.HandleEvent(recapEvent("run-test", "已完成本地检查。"))
		if string(*shell.Line()) != draft || shell.Cursor().Pos() != 6 {
			t.Fatalf("draft/cursor changed: %q at %d", string(*shell.Line()), shell.Cursor().Pos())
		}
		text := output.StripANSI(raw.String())
		if summary, prompt := strings.Index(text, "✻ 已完成本地检查。"), strings.LastIndex(text, "cyber-scan> "); summary < 0 || prompt <= summary {
			t.Fatalf("recap did not appear above prompt: %q", text)
		}
	}
}

func TestRecapRemoteConsoleUsesPromptWriter(t *testing.T) {
	rt, _ := newConsoleRuntime(t, nil)
	session, err := rt.OpenSession(context.Background(), agentsession.SessionOptions{ID: "recap-remote"})
	if err != nil {
		t.Fatal(err)
	}
	var raw syncedBuffer
	terminal := rlterm.Stream(strings.NewReader(""), &raw, &raw, rlterm.NewControl(true, 80, 24))
	r := newAgentConsole(context.Background(), rt, session, &cfg.Option{}, terminal, testSessionBindings(t, rt))
	defer r.Close()
	if r.readlineBridge == nil || r.output.recapWriter != r.readlineBridge {
		t.Fatal("remote recap is not connected to readline")
	}
	if !r.output.readline || r.stdout != r.readlineBridge || r.stderr != r.readlineBridge {
		t.Fatal("remote output is not coordinated with the readline editor")
	}
}

func TestRecapRespectsQuietAndStaticOutput(t *testing.T) {
	for _, verbosity := range []int{-1, 1} {
		var stderr bytes.Buffer
		o := testOutput(&stderr, verbosity, false)
		if verbosity < 0 {
			o.mode = ModeInteractive
		} else {
			o.mode = ModeStatic
		}
		o.HandleEvent(turnStartEvent(1))
		o.HandleEvent(turnEndEvent(1, 0))
		o.HandleEvent(recapEvent("run-test", "HIDDEN_RECAP"))
		if strings.Contains(stderr.String(), "HIDDEN_RECAP") {
			t.Fatal("recap ignored presentation policy")
		}
	}
}
