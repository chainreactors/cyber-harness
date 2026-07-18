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
}

func NewWriter(w io.Writer, agentName string) *Writer {
	return &Writer{w: w, agentName: agentName}
}

// WriteEvent serializes an AOP event as one JSONL line.
func (w *Writer) WriteEvent(ev Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seq++
	ev.Seq = w.seq
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.w.Write(data)
	return err
}

// HandleEvent converts an agent.Event to AOP events and writes them.
// Compatible with eventbus.Subscribe(writer.HandleEvent).
func (w *Writer) HandleEvent(ev agent.Event) {
	for _, aopEvent := range FromAgentEvent(ev, w.agentName) {
		_ = w.WriteEvent(aopEvent)
	}
}
