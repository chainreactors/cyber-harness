package output

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
	"google.golang.org/protobuf/encoding/protojson"
)

// ScanJSONL decodes the canonical append-only AOP event stream one line at a
// time. Blank lines are allowed; every non-blank line must be a complete AOP
// event with a session and payload.
func ScanJSONL(path string, visit func(*aop.Event) error) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open AOP JSONL: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 256*1024), 64*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if line[0] != '{' {
			return fmt.Errorf("AOP JSONL contains a non-event line")
		}
		event := new(aop.Event)
		if err := protojson.Unmarshal(line, event); err != nil {
			return fmt.Errorf("decode AOP JSONL event: %w", err)
		}
		if event.Id == "" || event.SessionId == "" || event.Payload == nil {
			return fmt.Errorf("AOP JSONL event is missing id, session_id or payload")
		}
		if visit != nil {
			if err := visit(event); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read AOP JSONL: %w", err)
	}
	return nil
}

func ReadJSONL(path string) ([]*aop.Event, error) {
	var events []*aop.Event
	err := ScanJSONL(path, func(event *aop.Event) error {
		events = append(events, event)
		return nil
	})
	return events, err
}

// JSONLRecorder is the single append-only subscriber for persisted AOP events.
type JSONLRecorder struct {
	mu   sync.Mutex
	file *os.File
	path string
	// seen is scoped to this recorder. Event IDs are unique within a session,
	// not necessarily across independent sessions, so the key includes both.
	seen  map[string]struct{}
	unsub func()
	err   error
}

func NewJSONLRecorder(bus *eventbus.Bus[*aop.Event], path string) (*JSONLRecorder, error) {
	if bus == nil {
		return nil, fmt.Errorf("AOP event bus is required")
	}
	recorder := &JSONLRecorder{seen: make(map[string]struct{})}
	if err := recorder.Switch(path); err != nil {
		return nil, err
	}
	recorder.unsub = bus.Subscribe(func(event *aop.Event) {
		if err := recorder.Write(event); err != nil {
			recorder.mu.Lock()
			if recorder.err == nil {
				recorder.err = err
			}
			recorder.mu.Unlock()
		}
	})
	return recorder, nil
}

func openJSONL(path string) (*os.File, string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return nil, "", fmt.Errorf("AOP JSONL path is required")
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, "", fmt.Errorf("create AOP JSONL directory: %w", err)
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("open AOP JSONL %s: %w", path, err)
	}
	return file, path, nil
}

func ValidateJSONLTarget(path string) error {
	file, _, err := openJSONL(path)
	if err != nil {
		return err
	}
	return file.Close()
}

func (r *JSONLRecorder) Switch(path string) error {
	file, clean, err := openJSONL(path)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	seen, err := loadJSONLIDs(clean)
	if err != nil {
		_ = file.Close()
		return err
	}
	old := r.file
	r.file = file
	r.path = clean
	r.seen = seen
	if old != nil {
		if err := old.Close(); err != nil {
			if r.err == nil {
				r.err = err
			}
		}
	}
	return nil
}

func loadJSONLIDs(path string) (map[string]struct{}, error) {
	seen := make(map[string]struct{})
	if err := ScanJSONL(path, func(event *aop.Event) error {
		if event.Id != "" {
			seen[event.SessionId+"\x00"+event.Id] = struct{}{}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return seen, nil
}

func (r *JSONLRecorder) Path() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.path
}

func (r *JSONLRecorder) Write(event *aop.Event) error {
	if r == nil || event == nil {
		return nil
	}
	if event.Id == "" || event.SessionId == "" || event.Payload == nil {
		return fmt.Errorf("AOP JSONL event requires id, session_id and payload")
	}
	line, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal AOP JSONL event: %w", err)
	}
	line = append(line, '\n')
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return io.ErrClosedPipe
	}
	key := event.SessionId + "\x00" + event.Id
	if _, exists := r.seen[key]; exists {
		return nil
	}
	n, err := r.file.Write(line)
	if err == nil && n != len(line) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return err
	}
	if r.seen == nil {
		r.seen = make(map[string]struct{})
	}
	r.seen[key] = struct{}{}
	return nil
}

func (r *JSONLRecorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	unsub := r.unsub
	r.unsub = nil
	file := r.file
	recordErr := r.err
	r.file = nil
	r.path = ""
	r.err = nil
	r.mu.Unlock()
	if unsub != nil {
		unsub()
	}
	if file != nil {
		if err := file.Close(); recordErr == nil {
			recordErr = err
		}
	}
	return recordErr
}
