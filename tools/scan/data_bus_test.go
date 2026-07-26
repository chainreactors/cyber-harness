package scan

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/tools/scan/engine"
	"github.com/chainreactors/utils/parsers"
)

func TestEmitStructuredDataPublishesScanAssets(t *testing.T) {
	bus := eventbus.New[output.ToolDataEvent]()
	cmd := New(&engine.Set{}, WithDataBus(bus))

	var events []output.ToolDataEvent
	unsub := bus.Subscribe(func(event output.ToolDataEvent) {
		events = append(events, event)
	})
	defer unsub()

	ctx := output.ContextWithCallID(context.Background(), "scan-call-1")
	cmd.emitStructuredData(ctx, &output.Result{
		Services: []*parsers.GOGOResult{{Ip: "127.0.0.1", Port: "8080", Protocol: "http"}},
		WebProbes: []*parsers.SprayResult{{
			UrlString: "http://127.0.0.1:8080/", Status: 200,
		}},
	})

	if len(events) != 2 {
		t.Fatalf("events = %d, want 2: %#v", len(events), events)
	}
	if events[0].Tool != "gogo" || events[0].Kind != output.ToolDataService {
		t.Fatalf("service event = %#v", events[0])
	}
	if events[1].Tool != "spray" || events[1].Kind != output.ToolDataWeb {
		t.Fatalf("web event = %#v", events[1])
	}
	for _, event := range events {
		if event.CallID != "scan-call-1" {
			t.Fatalf("call id = %q, want scan-call-1", event.CallID)
		}
	}
}
