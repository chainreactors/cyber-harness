//go:build e2e

package harness

import "testing"

func TestAgentBackgroundTask(t *testing.T) {
	h := New(t)
	r := h.Agent("Start a background shell session: tmux new -d -s bg 'sleep 1 && echo bg_done'. Then use tmux ls to list running sessions. Use tmux wait -t bg to wait for it to finish. Use tmux capture-pane -t bg to get the output. Report the final output.")
	Verify(t, r).
		OK().
		ToolUsed("bash").
		AnyResultContains("bg_done").
		NoToolErrors().
		Done()
}

func TestAgentTmuxPeek(t *testing.T) {
	h := New(t)
	r := h.Agent("Run 'for i in 1 2 3; do echo line_$i; sleep 0.5; done' as a detached tmux session named 'lines'. Use tmux capture-pane -t lines --new to check its output, then wait for completion and report all lines.")
	Verify(t, r).
		OK().
		ToolUsed("bash").
		Done()
}

func TestAgentTmuxKill(t *testing.T) {
	h := New(t)
	r := h.Agent("Start a detached tmux session: tmux new -d -s sleeper 'sleep 300'. Use tmux ls to confirm it's running. Kill it with tmux kill -t sleeper. List again to confirm it's killed. Report status.")
	Verify(t, r).
		OK().
		ToolUsed("bash").
		Done()
}
