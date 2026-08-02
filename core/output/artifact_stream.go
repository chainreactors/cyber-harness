package output

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
)

// ToolArtifact is one scanner-native structured record. Nodes forward it
// unchanged; only the server normalizes it into canonical SCO documents.
type ToolArtifact struct {
	Tool      string
	Kind      string
	Target    string
	Data      json.RawMessage
	CallID    string
	Timestamp time.Time
}

// ArtifactStream serializes structured ToolData events and exposes one
// lifecycle-bound handler for either a node transport or the local server.
type ArtifactStream struct {
	mu      sync.RWMutex
	unsub   func()
	handler func(ToolArtifact)
}

func NewArtifactStream(bus *eventbus.Bus[ToolDataEvent]) *ArtifactStream {
	stream := &ArtifactStream{}
	if bus != nil {
		stream.unsub = bus.Subscribe(stream.handle)
	}
	return stream
}

func (s *ArtifactStream) handle(event ToolDataEvent) {
	if event.Kind == ToolDataProgress || event.Data == nil {
		return
	}
	data, err := json.Marshal(event.Data)
	if err != nil {
		return
	}
	s.mu.RLock()
	handler := s.handler
	s.mu.RUnlock()
	if handler == nil {
		return
	}
	handler(ToolArtifact{
		Tool:      event.Tool,
		Kind:      event.Kind,
		Target:    event.Target,
		Data:      data,
		CallID:    event.CallID,
		Timestamp: event.Timestamp,
	})
}

func (s *ArtifactStream) SetHandler(handler func(ToolArtifact)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.handler = handler
	s.mu.Unlock()
}

func (s *ArtifactStream) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	unsub := s.unsub
	s.unsub = nil
	s.handler = nil
	s.mu.Unlock()
	if unsub != nil {
		unsub()
	}
}
