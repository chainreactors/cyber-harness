// Package eventoutput persists the canonical AOP event stream as JSONL.
package eventoutput

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
	coreevents "github.com/chainreactors/aiscan/core/events"
	"github.com/chainreactors/aiscan/core/extension"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	defaultQueue = 512
	defaultBytes = 16 << 20
)

type Options struct {
	Path     string
	Queue    int
	MaxBytes int64
}

type Extension struct {
	events  *coreevents.Stream
	options Options

	lifecycle sync.Mutex
	mu        sync.Mutex
	file      *os.File
	path      string
	sub       *eventbus.Subscription[*aop.Event]
	err       error
	loaded    bool
	closing   bool
}

var _ extension.Extension = (*Extension)(nil)

func New(events *coreevents.Stream, options Options) (*Extension, error) {
	if events == nil {
		return nil, fmt.Errorf("event output requires an AOP event stream")
	}
	if strings.TrimSpace(options.Path) == "" {
		return nil, fmt.Errorf("event output path is required")
	}
	if options.Queue <= 0 {
		options.Queue = defaultQueue
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = defaultBytes
	}
	return &Extension{events: events, options: options}, nil
}

func open(path string) (*os.File, string, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "." || clean == "" {
		return nil, "", fmt.Errorf("event output path is required")
	}
	if directory := filepath.Dir(clean); directory != "." && directory != "" {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return nil, "", fmt.Errorf("create event output directory: %w", err)
		}
	}
	file, err := os.OpenFile(clean, os.O_APPEND|os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("open event output: %w", err)
	}
	return file, clean, nil
}

func (e *Extension) Load(scope *extension.Scope) error {
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()
	if e.closing {
		return io.ErrClosedPipe
	}
	if e.loaded {
		return nil
	}
	if err := scope.Init().Err(); err != nil {
		return err
	}
	file, path, err := open(e.options.Path)
	if err != nil {
		return err
	}
	e.file, e.path = file, path
	e.sub, err = e.events.Consume(eventbus.SubscribeOptions[*aop.Event]{
		Buffer: e.options.Queue, MaxBytes: e.options.MaxBytes,
		Size: func(event *aop.Event) int64 { return int64(proto.Size(event)) },
		Clone: func(event *aop.Event) *aop.Event {
			if event == nil {
				return nil
			}
			return proto.Clone(event).(*aop.Event)
		},
	}, e)
	if err != nil {
		_ = file.Close()
		e.file = nil
		e.path = ""
		return err
	}
	e.loaded = true
	return nil
}

func (e *Extension) ConsumeEvent(event *aop.Event) error {
	if event == nil || event.Id == "" || event.Payload == nil {
		return fmt.Errorf("event output requires id and typed payload")
	}
	line, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event output: %w", err)
	}
	line = append(line, '\n')
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.file == nil {
		return io.ErrClosedPipe
	}
	n, err := e.file.Write(line)
	if err == nil && n != len(line) {
		err = io.ErrShortWrite
	}
	if err != nil && e.err == nil {
		e.err = err
	}
	return err
}

func (e *Extension) Path() string {
	if e == nil {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.path
}

// Flush makes every event admitted before the call durable enough for readers
// of the current file to observe it. It does not stop later event admission.
func (e *Extension) Flush(ctx context.Context) error {
	if e == nil {
		return nil
	}
	e.lifecycle.Lock()
	sub := e.sub
	e.lifecycle.Unlock()
	if sub == nil {
		return nil
	}
	if err := sub.Flush(ctx); err != nil {
		return err
	}
	if err := sub.Err(); err != nil {
		return err
	}
	if dropped := sub.Dropped(); dropped > 0 {
		return fmt.Errorf("event output incomplete: %d events dropped", dropped)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	if e.file == nil {
		return io.ErrClosedPipe
	}
	return e.file.Sync()
}

func (e *Extension) Close(ctx context.Context) error {
	if e == nil {
		return nil
	}
	e.lifecycle.Lock()
	e.closing = true
	sub := e.sub
	e.lifecycle.Unlock()
	if sub != nil {
		if err := sub.Close(ctx); err != nil {
			return err
		}
	}
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	err := e.err
	if sub != nil {
		err = errors.Join(err, sub.Err())
		if dropped := sub.Dropped(); dropped > 0 {
			err = errors.Join(err, fmt.Errorf("event output incomplete: %d events dropped", dropped))
		}
	}
	if e.file != nil {
		err = errors.Join(err, e.file.Sync(), e.file.Close())
	}
	e.file, e.path = nil, ""
	return err
}

var _ coreevents.Consumer = (*Extension)(nil)
