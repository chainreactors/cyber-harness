package aop

import (
	"encoding/json"
	"io"
	"sync"

	"github.com/chainreactors/aiscan/pkg/agent"
)

// Writer writes AOP events as JSONL to an io.Writer.
type Writer struct {
	w         io.Writer
	mu        sync.Mutex
	seq       int
	agentName string
	err       error
}

func NewWriter(w io.Writer, agentName string) *Writer {
	return &Writer{w: w, agentName: agentName}
}

func (w *Writer) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// WriteEvent serializes an AOP event as one JSONL line.
func (w *Writer) WriteEvent(ev Event) error {
	w.mu.Lock()
	if w.err != nil {
		err := w.err
		w.mu.Unlock()
		return err
	}
	w.seq++
	ev.Seq = w.seq
	data, err := json.Marshal(ev)
	if err != nil {
		w.err = err
		w.mu.Unlock()
		return err
	}
	data = append(data, '\n')
	_, err = w.w.Write(data)
	if err != nil {
		w.err = err
	}
	w.mu.Unlock()
	return err
}

// WriteAgentEvent converts and writes every AOP event derived from ev.
func (w *Writer) WriteAgentEvent(ev agent.Event) error {
	for _, aopEvent := range FromAgentEvent(ev, w.agentName) {
		if err := w.WriteEvent(aopEvent); err != nil {
			return err
		}
	}
	return nil
}

// HandleEvent converts an agent.Event to AOP events and writes them.
// Compatible with eventbus.Subscribe(writer.HandleEvent).
func (w *Writer) HandleEvent(ev agent.Event) {
	_ = w.WriteAgentEvent(ev)
}
