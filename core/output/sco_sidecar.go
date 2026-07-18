package output

import (
	"encoding/json"
	"sync"

	"github.com/chainreactors/aiscan/core/eventbus"
)

type SCOTransformFunc func(tool string, data any) ([]json.RawMessage, error)

type SCOSidecar struct {
	mu        sync.Mutex
	seen      map[string]struct{}
	nodes     []json.RawMessage
	unsub     func()
	transform SCOTransformFunc
	OnNodes   func(callID string, nodes []json.RawMessage)
}

func NewSCOSidecar(bus *eventbus.Bus[ToolDataEvent], transform SCOTransformFunc) *SCOSidecar {
	if transform == nil {
		return &SCOSidecar{}
	}
	s := &SCOSidecar{
		seen:      make(map[string]struct{}),
		transform: transform,
	}
	s.unsub = bus.Subscribe(s.handle)
	return s
}

func (s *SCOSidecar) handle(ev ToolDataEvent) {
	if s.transform == nil {
		return
	}
	rawNodes, err := s.transform(ev.Tool, ev.Data)
	if err != nil || len(rawNodes) == 0 {
		return
	}

	var fresh []json.RawMessage
	s.mu.Lock()
	for _, raw := range rawNodes {
		var header struct {
			ID string `json:"cstx_id"`
		}
		if json.Unmarshal(raw, &header) != nil || header.ID == "" {
			continue
		}
		if _, dup := s.seen[header.ID]; dup {
			continue
		}
		s.seen[header.ID] = struct{}{}
		s.nodes = append(s.nodes, raw)
		fresh = append(fresh, raw)
	}
	cb := s.OnNodes
	s.mu.Unlock()

	if cb != nil && len(fresh) > 0 {
		cb(ev.CallID, fresh)
	}
}

func (s *SCOSidecar) Snapshot() []json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]json.RawMessage, len(s.nodes))
	copy(out, s.nodes)
	return out
}

func (s *SCOSidecar) Close() {
	if s.unsub != nil {
		s.unsub()
		s.unsub = nil
	}
}
