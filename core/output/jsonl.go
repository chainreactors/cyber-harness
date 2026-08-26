package output

import (
	"bufio"
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
// time. Empty and non-JSON lines are ignored; malformed AOP event lines fail.
func ScanJSONL(path string, visit func(*aop.Event) error) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open AOP JSONL: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 256*1024), 64*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		event := new(aop.Event)
		if err := protojson.Unmarshal(line, event); err != nil {
			return fmt.Errorf("decode AOP JSONL event: %w", err)
		}
		if event.SessionId == "" || event.Payload == nil {
			continue
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

// DefaultRecorderMaxBytes caps one session's JSONL file. Legitimate sessions
// stay in the megabytes; the cap is a last-line defense so a runaway event
// source (a retry loop once emitted turn lifecycle pairs at microsecond
// cadence and wrote 182GB) cannot fill the disk. It is a capacity guard, not
// content policy: no event inspection or deduplication happens here.
const DefaultRecorderMaxBytes int64 = 2 << 30 // 2 GiB

// JSONLRecorder is the single append-only subscriber for persisted AOP events.
type JSONLRecorder struct {
	mu       sync.Mutex
	file     *os.File
	path     string
	unsub    func()
	err      error
	maxBytes int64
	size     int64
	limited  bool
}

func NewJSONLRecorder(bus *eventbus.Bus[*aop.Event], path string) (*JSONLRecorder, error) {
	if bus == nil {
		return nil, fmt.Errorf("AOP event bus is required")
	}
	recorder := &JSONLRecorder{maxBytes: DefaultRecorderMaxBytes}
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
	// The file is opened in append mode, so a resumed session starts with its
	// existing size already counted against the cap.
	var size int64
	if info, statErr := file.Stat(); statErr == nil {
		size = info.Size()
	}
	r.mu.Lock()
	old := r.file
	r.file = file
	r.path = clean
	r.size = size
	r.limited = r.maxBytes > 0 && size >= r.maxBytes
	r.mu.Unlock()
	if old != nil {
		if err := old.Close(); err != nil {
			r.mu.Lock()
			if r.err == nil {
				r.err = err
			}
			r.mu.Unlock()
		}
	}
	return nil
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
	// Fast path once the cap has tripped: skip the marshal so an ongoing
	// event storm costs almost nothing per dropped event.
	r.mu.Lock()
	limited := r.limited
	r.mu.Unlock()
	if limited {
		return nil
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
	if r.limited {
		return nil
	}
	if r.maxBytes > 0 && r.size+int64(len(line)) > r.maxBytes {
		r.limited = true
		// Leave one non-JSON marker line as durable evidence of the
		// truncation; ScanJSONL skips lines that do not start with '{', so
		// the file stays readable and resumable.
		marker := fmt.Sprintf("# aiscan: JSONL size limit reached (limit=%d bytes); subsequent events are dropped\n", r.maxBytes)
		_, _ = r.file.Write([]byte(marker))
		// The bus subscription stores the first Write error, so this
		// surfaces once through Close instead of once per dropped event.
		return fmt.Errorf("AOP JSONL %s reached the %d-byte size limit; subsequent events are dropped", r.path, r.maxBytes)
	}
	n, err := r.file.Write(line)
	if err == nil && n != len(line) {
		err = io.ErrShortWrite
	}
	if err == nil {
		r.size += int64(n)
	}
	return err
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
