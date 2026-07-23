package tui

import (
	"bytes"
	"reflect"
	"testing"
)

func TestReadlineConsoleBridgeCommitsCompleteLines(t *testing.T) {
	active := true
	var committed []string
	bridge := &readlineConsoleBridge{
		active: func() bool { return active },
		commit: func(text string) error {
			committed = append(committed, text)
			return nil
		},
	}

	if _, err := bridge.Write([]byte("hello")); err != nil {
		t.Fatalf("write fragment: %v", err)
	}
	if len(committed) != 0 {
		t.Fatalf("fragment committed early: %#v", committed)
	}

	if _, err := bridge.Write([]byte(" world\nnext")); err != nil {
		t.Fatalf("write completed line: %v", err)
	}
	if want := []string{"hello world"}; !reflect.DeepEqual(committed, want) {
		t.Fatalf("committed = %#v, want %#v", committed, want)
	}

	if _, err := bridge.Write([]byte("\n")); err != nil {
		t.Fatalf("finish remainder: %v", err)
	}
	if want := []string{"hello world", "next"}; !reflect.DeepEqual(committed, want) {
		t.Fatalf("committed = %#v, want %#v", committed, want)
	}
}

func TestReadlineConsoleBridgePreservesMultilineBatches(t *testing.T) {
	var committed []string
	bridge := &readlineConsoleBridge{
		active: func() bool { return true },
		commit: func(text string) error {
			committed = append(committed, text)
			return nil
		},
	}

	if _, err := bridge.Write([]byte("one\r\ntwo\nthree")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if want := []string{"one\ntwo"}; !reflect.DeepEqual(committed, want) {
		t.Fatalf("committed = %#v, want %#v", committed, want)
	}
	if _, err := bridge.Write([]byte("\n")); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if want := []string{"one\ntwo", "three"}; !reflect.DeepEqual(committed, want) {
		t.Fatalf("committed = %#v, want %#v", committed, want)
	}
}

func TestReadlineConsoleBridgeWritesDirectlyWhenInactive(t *testing.T) {
	active := true
	var raw bytes.Buffer
	bridge := &readlineConsoleBridge{
		raw:    &raw,
		active: func() bool { return active },
		commit: func(string) error { return nil },
	}

	if _, err := bridge.Write([]byte("pending")); err != nil {
		t.Fatalf("buffer pending: %v", err)
	}
	active = false
	if _, err := bridge.Write([]byte(" direct\n")); err != nil {
		t.Fatalf("direct write: %v", err)
	}
	if got, want := raw.String(), "pending direct\n"; got != want {
		t.Fatalf("raw = %q, want %q", got, want)
	}
}

func TestReadlineConsoleBridgeStatusOnlyUpdatesWhileActive(t *testing.T) {
	active := false
	redraws := 0
	bridge := &readlineConsoleBridge{
		active: func() bool { return active },
		redraw: func() { redraws++ },
	}

	bridge.UpdateStatus("hidden")
	if got := bridge.Status(); got != "hidden" {
		t.Fatalf("stored status = %q, want hidden", got)
	}
	if redraws != 0 {
		t.Fatalf("inactive status triggered %d redraws", redraws)
	}
	active = true
	bridge.SetReady(true)
	bridge.UpdateStatus("thinking")
	bridge.UpdateStatus("")

	if redraws != 2 {
		t.Fatalf("active status redraws = %d, want 2", redraws)
	}
}

func TestReadlineConsoleBridgeRedrawsStatusArrivingDuringPromptStartup(t *testing.T) {
	redraws := 0
	bridge := &readlineConsoleBridge{
		active: func() bool { return true },
		redraw: func() { redraws++ },
	}

	_ = bridge.Status() // primary prompt observed the pre-turn state
	bridge.UpdateStatus("thinking")
	if redraws != 0 {
		t.Fatalf("status redrew before prompt was ready: %d", redraws)
	}
	bridge.SetReady(true)
	if redraws != 1 {
		t.Fatalf("ready transition redraws = %d, want 1", redraws)
	}
}

func TestReadlineConsoleBridgeKeepsLatestInactiveStatusForNextPrompt(t *testing.T) {
	bridge := &readlineConsoleBridge{active: func() bool { return false }}
	bridge.UpdateStatus("thinking")
	if got := bridge.Status(); got != "thinking" {
		t.Fatalf("status = %q, want thinking", got)
	}
}
