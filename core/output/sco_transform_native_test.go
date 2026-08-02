//go:build cstx_native

package output

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/utils/parsers"
)

func TestE2E_GogoThroughCSTXTransform(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	sidecar := NewSCOSidecar(bus, CSTXTransform)
	defer sidecar.Close()

	var received []json.RawMessage
	sidecar.OnNodes = func(_ string, nodes []json.RawMessage) {
		received = append(received, nodes...)
	}

	bus.Emit(ToolDataEvent{
		Tool: "gogo", Kind: ToolDataService, Target: "192.168.1.1:80",
		Data: &parsers.GOGOResult{
			Ip: "192.168.1.1", Port: "80", Protocol: "tcp",
			Status: "200", Title: "Admin", Midware: "nginx",
			Frameworks: parsers.Frameworks{
				"nginx": &parsers.Framework{Name: "nginx"},
			},
		},
		Timestamp: time.Now(),
	})

	if len(received) == 0 {
		t.Fatal("expected SCO nodes from gogo result, got none")
	}

	types := collectTypes(t, received)
	for _, required := range []string{"ip", "port", "app"} {
		if types[required] == 0 {
			t.Errorf("missing expected node type %q from gogo", required)
		}
	}
	t.Logf("gogo -> %d nodes, types: %v", len(received), types)
}

func TestE2E_SprayThroughCSTXTransform(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	sidecar := NewSCOSidecar(bus, CSTXTransform)
	defer sidecar.Close()

	var received []json.RawMessage
	sidecar.OnNodes = func(_ string, nodes []json.RawMessage) {
		received = append(received, nodes...)
	}

	bus.Emit(ToolDataEvent{
		Tool: "spray", Kind: ToolDataWeb, Target: "http://example.com/",
		Data: &parsers.SprayResult{
			UrlString: "http://example.com/", Status: 200,
			Host: "example.com", Path: "/",
			ContentType: "text/html", BodyLength: 5000, Title: "Example",
		},
		Timestamp: time.Now(),
	})

	if len(received) == 0 {
		t.Fatal("expected SCO nodes from spray result, got none")
	}

	types := collectTypes(t, received)
	if types["url"] == 0 && types["app"] == 0 {
		t.Error("expected url or app node from spray result")
	}
	t.Logf("spray -> %d nodes, types: %v", len(received), types)
}

func TestE2E_ZombieThroughCSTXTransform(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	sidecar := NewSCOSidecar(bus, CSTXTransform)
	defer sidecar.Close()

	var received []json.RawMessage
	sidecar.OnNodes = func(_ string, nodes []json.RawMessage) {
		received = append(received, nodes...)
	}

	bus.Emit(ToolDataEvent{
		Tool: "zombie", Kind: ToolDataWeakpass, Target: "10.0.0.1:22",
		Data: &parsers.ZombieResult{
			IP: "10.0.0.1", Port: "22", Service: "ssh",
			Username: "root", Password: "toor",
		},
		Timestamp: time.Now(),
	})

	if len(received) == 0 {
		t.Fatal("expected SCO nodes from zombie result, got none")
	}

	types := collectTypes(t, received)
	if types["vuln"] == 0 {
		t.Error("expected vuln node from zombie weak password result")
	}
	t.Logf("zombie -> %d nodes, types: %v", len(received), types)
}

func TestE2E_AllToolsMixed(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	sidecar := NewSCOSidecar(bus, CSTXTransform)
	defer sidecar.Close()

	var received []json.RawMessage
	sidecar.OnNodes = func(_ string, nodes []json.RawMessage) {
		received = append(received, nodes...)
	}

	bus.Emit(ToolDataEvent{
		Tool: "gogo", Kind: ToolDataService, Target: "10.0.0.1:443",
		Data: &parsers.GOGOResult{
			Ip: "10.0.0.1", Port: "443", Protocol: "https",
			Status: "200", Title: "Dashboard",
		},
		Timestamp: time.Now(),
	})

	bus.Emit(ToolDataEvent{
		Tool: "spray", Kind: ToolDataWeb, Target: "https://10.0.0.1/api",
		Data: &parsers.SprayResult{
			UrlString: "https://10.0.0.1/api", Status: 200,
			Host: "10.0.0.1", Path: "/api", ContentType: "application/json",
		},
		Timestamp: time.Now(),
	})

	bus.Emit(ToolDataEvent{
		Tool: "zombie", Kind: ToolDataWeakpass, Target: "10.0.0.1:3306",
		Data: &parsers.ZombieResult{
			IP: "10.0.0.1", Port: "3306", Service: "mysql",
			Username: "root", Password: "",
		},
		Timestamp: time.Now(),
	})

	if len(received) == 0 {
		t.Fatal("expected SCO nodes from mixed tool pipeline")
	}

	types := collectTypes(t, received)
	t.Logf("mixed pipeline -> %d total nodes, types: %v", len(received), types)

	if types["ip"] == 0 {
		t.Error("expected ip node")
	}
	if types["port"] == 0 {
		t.Error("expected port node")
	}

	snap := sidecar.Snapshot()
	if len(snap) != len(received) {
		t.Errorf("snapshot len %d != received len %d", len(snap), len(received))
	}
}

func TestE2E_DedupAcrossTools(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	sidecar := NewSCOSidecar(bus, CSTXTransform)
	defer sidecar.Close()

	bus.Emit(ToolDataEvent{
		Tool: "gogo", Kind: ToolDataService, Target: "10.0.0.1:80",
		Data: &parsers.GOGOResult{Ip: "10.0.0.1", Port: "80", Protocol: "tcp"},
	})

	countAfterFirst := len(sidecar.Snapshot())

	bus.Emit(ToolDataEvent{
		Tool: "gogo", Kind: ToolDataService, Target: "10.0.0.1:80",
		Data: &parsers.GOGOResult{Ip: "10.0.0.1", Port: "80", Protocol: "tcp"},
	})

	countAfterDup := len(sidecar.Snapshot())
	if countAfterDup != countAfterFirst {
		t.Errorf("dedup failed: %d after first, %d after dup", countAfterFirst, countAfterDup)
	}

	bus.Emit(ToolDataEvent{
		Tool: "gogo", Kind: ToolDataService, Target: "10.0.0.2:80",
		Data: &parsers.GOGOResult{Ip: "10.0.0.2", Port: "80", Protocol: "tcp"},
	})

	countAfterNew := len(sidecar.Snapshot())
	if countAfterNew <= countAfterDup {
		t.Errorf("new target should produce new nodes: %d <= %d", countAfterNew, countAfterDup)
	}
	t.Logf("dedup: first=%d, dup=%d, new_target=%d", countAfterFirst, countAfterDup, countAfterNew)
}

func collectTypes(t *testing.T, nodes []json.RawMessage) map[string]int {
	t.Helper()
	types := make(map[string]int)
	for _, raw := range nodes {
		var h struct {
			Type string `json:"cstx_type"`
			ID   string `json:"cstx_id"`
		}
		if err := json.Unmarshal(raw, &h); err != nil {
			t.Errorf("unmarshal node: %v", err)
			continue
		}
		types[h.Type]++
		t.Logf("  %s %s", h.Type, h.ID)
	}
	return types
}
