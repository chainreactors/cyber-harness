package output

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// TimelineWriter writes Record entries to a single JSONL file.
type TimelineWriter struct {
	mu   sync.Mutex
	file *os.File
}

func NewTimelineWriter(path string) (*TimelineWriter, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open timeline file %s: %w", path, err)
	}
	return &TimelineWriter{file: f}, nil
}

func (w *TimelineWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *TimelineWriter) WriteRecord(rec Record) {
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	line = append(line, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return
	}
	_, _ = w.file.Write(line)
}
