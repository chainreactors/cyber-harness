package output

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	aop "github.com/chainreactors/cyber/aop"
	"google.golang.org/protobuf/encoding/protojson"
)

// ScanJSONL decodes the canonical append-only AOP event stream one line at a
// time. Blank lines are allowed; every non-blank line must be a complete AOP
// event with an ID and payload. Session-less root observations are valid.
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
		if event.Id == "" || event.Payload == nil {
			return fmt.Errorf("AOP JSONL event is missing id or payload")
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

func ValidateJSONLTarget(path string) error {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return fmt.Errorf("AOP JSONL path is required")
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create AOP JSONL directory: %w", err)
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open AOP JSONL %s: %w", path, err)
	}
	return file.Close()
}
