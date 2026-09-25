package proxy

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// sseReader wraps an io.Reader to parse SSE events and trigger hooks
type sseReader struct {
	flow    *Flow
	proxy   *Proxy
	reader  *bufio.Reader
	buffer  []byte // accumulated data for current event
	started bool
	ended   bool
	mu      sync.Mutex
}

// newSSEReader creates a new SSE reader wrapper
func newSSEReader(f *Flow, r io.Reader) io.Reader {
	return &sseReader{
		flow:   f,
		proxy:  f.ConnContext.proxy,
		reader: bufio.NewReader(r),
		buffer: make([]byte, 0, 1024),
	}
}

// Read implements io.Reader, parsing SSE events as data is read
func (sr *sseReader) Read(p []byte) (n int, err error) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if sr.ended {
		return 0, io.EOF
	}

	n, err = sr.reader.Read(p)
	if n > 0 {
		sr.buffer = append(sr.buffer, p[:n]...)
		sr.parseEvents()
	}

	if err != nil {
		if sr.started {
			sr.flushEvent()
			sr.triggerEnd()
		}
		return n, err
	}

	if !sr.started {
		sr.started = true
	}

	return n, nil
}

// parseEvents parses SSE events from the buffer
func (sr *sseReader) parseEvents() {
	for {
		eventEnd := sr.findEventEnd()
		if eventEnd == -1 {
			break
		}

		eventData := string(sr.buffer[:eventEnd])
		if len(eventData) > 0 {
			sr.parseAndFlushEvent(eventData)
		}

		sr.buffer = sr.buffer[eventEnd+2:]
	}
}

func (sr *sseReader) findEventEnd() int {
	for i := 0; i < len(sr.buffer)-1; i++ {
		if sr.buffer[i] == '\n' && sr.buffer[i+1] == '\n' {
			return i
		}
	}
	return -1
}

func (sr *sseReader) parseAndFlushEvent(eventData string) {
	event := sr.parseEvent(eventData)

	if event.Data != "" {
		sr.flow.SSE.addEvent(event)

		for _, addon := range sr.proxy.Addons {
			addon.SSEMessage(sr.flow)
		}
	}
}

func (sr *sseReader) flushEvent() {
	if len(sr.buffer) == 0 {
		return
	}

	event := sr.parseEvent(string(sr.buffer))

	if event.Data != "" {
		sr.flow.SSE.addEvent(event)

		for _, addon := range sr.proxy.Addons {
			addon.SSEMessage(sr.flow)
		}
	}

	sr.buffer = sr.buffer[:0]
}

func (sr *sseReader) parseEvent(text string) *SSEEvent {
	event := &SSEEvent{
		Event: "message",
		Data:  "",
		Raw:   text,
		Time:  time.Now(),
	}

	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, ":") {
			continue
		}

		if idx := strings.Index(line, ":"); idx != -1 {
			field := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])

			switch field {
			case "data":
				if event.Data != "" {
					event.Data += "\n"
				}
				event.Data += value
			case "event":
				event.Event = value
			case "id":
				event.ID = value
			case "retry":
				if retry, err := strconv.Atoi(value); err == nil {
					event.Retry = retry
				}
			}
		}
	}

	return event
}

func (sr *sseReader) triggerEnd() {
	if sr.ended {
		return
	}

	sr.ended = true

	for _, addon := range sr.proxy.Addons {
		addon.SSEEnd(sr.flow)
	}

	log.Debugf("SSE stream ended for %s", sr.flow.Request.URL.String())
}
