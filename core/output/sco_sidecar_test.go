package output

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/utils/parsers"
)

func mockTransform(tool string, data any) ([]json.RawMessage, error) {
	type node struct {
		CstxType string `json:"cstx_type"`
		CstxID   string `json:"cstx_id"`
		Tool     string `json:"tool"`
	}

	switch tool {
	case "gogo":
		r, ok := data.(*parsers.GOGOResult)
		if !ok {
			return nil, nil
		}
		n := node{CstxType: "port", CstxID: "port:" + r.Ip + ":" + r.Port, Tool: tool}
		raw, _ := json.Marshal(n)
		return []json.RawMessage{raw}, nil
	case "spray":
		r, ok := data.(*parsers.SprayResult)
		if !ok {
			return nil, nil
		}
		n := node{CstxType: "url", CstxID: "url:" + r.UrlString, Tool: tool}
		raw, _ := json.Marshal(n)
		return []json.RawMessage{raw}, nil
	case "neutron":
		n := node{CstxType: "vuln", CstxID: "vuln:neutron:" + tool, Tool: tool}
		raw, _ := json.Marshal(n)
		return []json.RawMessage{raw}, nil
	case "katana":
		n := node{CstxType: "endpoint", CstxID: "endpoint:katana:" + tool, Tool: tool}
		raw, _ := json.Marshal(n)
		return []json.RawMessage{raw}, nil
	case "proton":
		n := node{CstxType: "secret", CstxID: "secret:proton:" + tool, Tool: tool}
		raw, _ := json.Marshal(n)
		return []json.RawMessage{raw}, nil
	}
	return nil, nil
}

func TestSCOSidecar_GogoEvent(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	sidecar := NewSCOSidecar(bus, mockTransform)
	defer sidecar.Close()

	var received []json.RawMessage
	var mu sync.Mutex
	sidecar.OnNodes = func(_ string, nodes []json.RawMessage) {
		mu.Lock()
		received = append(received, nodes...)
		mu.Unlock()
	}

	bus.Emit(ToolDataEvent{
		Tool:      "gogo",
		Kind:      ToolDataService,
		Target:    "192.168.1.1:80",
		Data:      &parsers.GOGOResult{Ip: "192.168.1.1", Port: "80", Protocol: "tcp"},
		Timestamp: time.Now(),
	})

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 node, got %d", len(received))
	}
	var header struct {
		Type string `json:"cstx_type"`
		ID   string `json:"cstx_id"`
	}
	json.Unmarshal(received[0], &header)
	if header.Type != "port" {
		t.Errorf("expected type=port, got %s", header.Type)
	}
	if header.ID != "port:192.168.1.1:80" {
		t.Errorf("expected id=port:192.168.1.1:80, got %s", header.ID)
	}
}

func TestSCOSidecar_SprayEvent(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	sidecar := NewSCOSidecar(bus, mockTransform)
	defer sidecar.Close()

	var received []json.RawMessage
	sidecar.OnNodes = func(_ string, nodes []json.RawMessage) {
		received = append(received, nodes...)
	}

	bus.Emit(ToolDataEvent{
		Tool:      "spray",
		Kind:      ToolDataWeb,
		Target:    "http://example.com/login",
		Data:      &parsers.SprayResult{UrlString: "http://example.com/login", Status: 200},
		Timestamp: time.Now(),
	})

	if len(received) != 1 {
		t.Fatalf("expected 1 node, got %d", len(received))
	}
	var header struct {
		ID string `json:"cstx_id"`
	}
	json.Unmarshal(received[0], &header)
	if header.ID != "url:http://example.com/login" {
		t.Errorf("unexpected id: %s", header.ID)
	}
}

func TestSCOSidecar_Dedup(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	sidecar := NewSCOSidecar(bus, mockTransform)
	defer sidecar.Close()

	var callbackCount int
	sidecar.OnNodes = func(_ string, nodes []json.RawMessage) {
		callbackCount += len(nodes)
	}

	ev := ToolDataEvent{
		Tool:      "gogo",
		Kind:      ToolDataService,
		Target:    "10.0.0.1:443",
		Data:      &parsers.GOGOResult{Ip: "10.0.0.1", Port: "443", Protocol: "tcp"},
		Timestamp: time.Now(),
	}
	bus.Emit(ev)
	bus.Emit(ev)
	bus.Emit(ev)

	if callbackCount != 1 {
		t.Errorf("expected 1 unique callback, got %d (dedup failed)", callbackCount)
	}
	snap := sidecar.Snapshot()
	if len(snap) != 1 {
		t.Errorf("expected 1 node in snapshot, got %d", len(snap))
	}
}

func TestSCOSidecar_MultiToolPipeline(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	sidecar := NewSCOSidecar(bus, mockTransform)
	defer sidecar.Close()

	var received []json.RawMessage
	sidecar.OnNodes = func(_ string, nodes []json.RawMessage) {
		received = append(received, nodes...)
	}

	bus.Emit(ToolDataEvent{
		Tool: "gogo", Kind: ToolDataService, Target: "10.0.0.1:22",
		Data:      &parsers.GOGOResult{Ip: "10.0.0.1", Port: "22", Protocol: "tcp"},
		Timestamp: time.Now(),
	})
	bus.Emit(ToolDataEvent{
		Tool: "spray", Kind: ToolDataWeb, Target: "http://10.0.0.1/",
		Data:      &parsers.SprayResult{UrlString: "http://10.0.0.1/", Status: 200},
		Timestamp: time.Now(),
	})
	bus.Emit(ToolDataEvent{
		Tool: "neutron", Kind: ToolDataVuln, Target: "10.0.0.1",
		Data:      map[string]string{"template": "cve-2024-0001"},
		Timestamp: time.Now(),
	})
	bus.Emit(ToolDataEvent{
		Tool: "katana", Kind: ToolDataWeb, Target: "http://10.0.0.1/api",
		Data:      map[string]string{"url": "http://10.0.0.1/api"},
		Timestamp: time.Now(),
	})
	bus.Emit(ToolDataEvent{
		Tool: "proton", Kind: ToolDataVuln, Target: "/app/config.yaml",
		Data:      map[string]string{"template_id": "aws-key"},
		Timestamp: time.Now(),
	})

	if len(received) != 5 {
		t.Fatalf("expected 5 nodes from 5 tools, got %d", len(received))
	}

	types := make(map[string]int)
	for _, raw := range received {
		var h struct {
			Type string `json:"cstx_type"`
		}
		json.Unmarshal(raw, &h)
		types[h.Type]++
	}

	expect := map[string]int{"port": 1, "url": 1, "vuln": 1, "endpoint": 1, "secret": 1}
	for k, v := range expect {
		if types[k] != v {
			t.Errorf("type %s: expected %d, got %d", k, v, types[k])
		}
	}
}

func TestSCOSidecar_NilTransform(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	sidecar := NewSCOSidecar(bus, nil)
	defer sidecar.Close()

	bus.Emit(ToolDataEvent{
		Tool: "gogo", Kind: ToolDataService,
		Data: &parsers.GOGOResult{Ip: "1.1.1.1", Port: "80"},
	})

	snap := sidecar.Snapshot()
	if len(snap) != 0 {
		t.Errorf("nil transform should produce no nodes, got %d", len(snap))
	}
}

func TestSCOSidecar_ConcurrentEmit(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	sidecar := NewSCOSidecar(bus, mockTransform)
	defer sidecar.Close()

	var mu sync.Mutex
	var received []json.RawMessage
	sidecar.OnNodes = func(_ string, nodes []json.RawMessage) {
		mu.Lock()
		received = append(received, nodes...)
		mu.Unlock()
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ip := fmt.Sprintf("10.0.%d.%d", idx/256, idx%256)
			bus.Emit(ToolDataEvent{
				Tool: "gogo", Kind: ToolDataService, Target: ip + ":80",
				Data:      &parsers.GOGOResult{Ip: ip, Port: "80", Protocol: "tcp"},
				Timestamp: time.Now(),
			})
		}(i)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 50 {
		t.Errorf("expected 50 unique nodes from concurrent emit, got %d", len(received))
	}
	snap := sidecar.Snapshot()
	if len(snap) != 50 {
		t.Errorf("snapshot expected 50, got %d", len(snap))
	}
}

func TestSCOSidecar_TransformError(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	errorTransform := func(tool string, data any) ([]json.RawMessage, error) {
		return nil, fmt.Errorf("transform failed")
	}
	sidecar := NewSCOSidecar(bus, errorTransform)
	defer sidecar.Close()

	var callbackCalled bool
	sidecar.OnNodes = func(_ string, nodes []json.RawMessage) {
		callbackCalled = true
	}

	bus.Emit(ToolDataEvent{
		Tool: "gogo", Kind: ToolDataService,
		Data: &parsers.GOGOResult{Ip: "1.1.1.1", Port: "80"},
	})

	if callbackCalled {
		t.Error("callback should not be called when transform returns error")
	}
	if len(sidecar.Snapshot()) != 0 {
		t.Error("no nodes should be accumulated on transform error")
	}
}

func TestSCOSidecar_EmptyNodeID(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	noIDTransform := func(tool string, data any) ([]json.RawMessage, error) {
		raw, _ := json.Marshal(map[string]string{"cstx_type": "port"})
		return []json.RawMessage{raw}, nil
	}
	sidecar := NewSCOSidecar(bus, noIDTransform)
	defer sidecar.Close()

	bus.Emit(ToolDataEvent{
		Tool: "gogo", Kind: ToolDataService,
		Data: &parsers.GOGOResult{Ip: "1.1.1.1", Port: "80"},
	})

	if len(sidecar.Snapshot()) != 0 {
		t.Error("nodes with empty cstx_id should be filtered out")
	}
}

func TestSCOSidecar_CallIDPassthrough(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	sidecar := NewSCOSidecar(bus, mockTransform)
	defer sidecar.Close()

	var gotCallID string
	sidecar.OnNodes = func(callID string, nodes []json.RawMessage) {
		gotCallID = callID
	}

	bus.Emit(ToolDataEvent{
		Tool:   "gogo",
		Kind:   ToolDataService,
		Target: "10.0.0.1:80",
		Data:   &parsers.GOGOResult{Ip: "10.0.0.1", Port: "80", Protocol: "tcp"},
		CallID: "call_abc123",
	})

	if gotCallID != "call_abc123" {
		t.Errorf("expected CallID=call_abc123, got %q", gotCallID)
	}
}

func TestSCOSidecar_Close(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	sidecar := NewSCOSidecar(bus, mockTransform)

	var count int
	sidecar.OnNodes = func(_ string, nodes []json.RawMessage) { count += len(nodes) }

	bus.Emit(ToolDataEvent{
		Tool: "gogo", Kind: ToolDataService,
		Data: &parsers.GOGOResult{Ip: "1.1.1.1", Port: "80"},
	})
	if count != 1 {
		t.Fatalf("expected 1 before close, got %d", count)
	}

	sidecar.Close()

	bus.Emit(ToolDataEvent{
		Tool: "gogo", Kind: ToolDataService,
		Data: &parsers.GOGOResult{Ip: "2.2.2.2", Port: "80"},
	})
	if count != 1 {
		t.Errorf("expected no new nodes after close, got %d", count)
	}
}
