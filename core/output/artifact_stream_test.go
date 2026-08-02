package output

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
)

func TestArtifactStreamForwardsStructuredRecords(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	stream := NewArtifactStream(bus)
	defer stream.Close()

	var got ToolArtifact
	stream.SetHandler(func(artifact ToolArtifact) { got = artifact })
	timestamp := time.Now()
	bus.Emit(ToolDataEvent{
		Tool: "gogo", Kind: ToolDataService, Target: "192.0.2.1:80",
		Data:   map[string]any{"ip": "192.0.2.1", "port": "80"},
		CallID: "scan-1", Timestamp: timestamp,
	})

	if got.Tool != "gogo" || got.Kind != ToolDataService || got.CallID != "scan-1" {
		t.Fatalf("unexpected artifact metadata: %+v", got)
	}
	var data map[string]string
	if err := json.Unmarshal(got.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["ip"] != "192.0.2.1" || data["port"] != "80" {
		t.Fatalf("unexpected artifact data: %v", data)
	}
}

func TestArtifactStreamIgnoresProgressAndStopsOnClose(t *testing.T) {
	bus := eventbus.New[ToolDataEvent]()
	stream := NewArtifactStream(bus)
	count := 0
	stream.SetHandler(func(ToolArtifact) { count++ })

	bus.Emit(ToolDataEvent{Kind: ToolDataProgress, Data: "running"})
	stream.Close()
	bus.Emit(ToolDataEvent{Tool: "gogo", Kind: ToolDataService, Data: map[string]string{"ip": "192.0.2.1"}})
	if count != 0 {
		t.Fatalf("handler called %d times", count)
	}
}
