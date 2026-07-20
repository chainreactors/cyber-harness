package webagent

import (
	"encoding/json"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

// DataPipeline subscribes to DataBus/SCO and forwards events over WS.
type DataPipeline struct {
	dataBus *eventbus.Bus[output.ToolDataEvent]
	sco     *output.SCOSidecar
	unsub   func()
}

// BuildDataPipeline creates a DataPipeline from optional DataBus and SCO sidecar.
// Returns nil when both inputs are nil.
func BuildDataPipeline(db *eventbus.Bus[output.ToolDataEvent], sco *output.SCOSidecar) *DataPipeline {
	if db == nil && sco == nil {
		return nil
	}
	return &DataPipeline{dataBus: db, sco: sco}
}

// Attach starts forwarding data events over the WebSocket send function.
func (p *DataPipeline) Attach(send func(webproto.Message)) {
	if p == nil {
		return
	}
	if p.dataBus != nil {
		p.unsub = p.dataBus.Subscribe(func(ev output.ToolDataEvent) {
			payload, _ := json.Marshal(ev)
			send(webproto.Message{Type: "tool.data", Payload: payload})
		})
	}
	if p.sco != nil {
		p.sco.OnNodes = func(callID string, nodes []json.RawMessage) {
			payload, _ := json.Marshal(map[string]any{"call_id": callID, "nodes": nodes})
			send(webproto.Message{Type: "tool.sco", Payload: payload})
		}
	}
}

// Detach stops forwarding data events and cleans up subscriptions.
func (p *DataPipeline) Detach() {
	if p == nil {
		return
	}
	if p.unsub != nil {
		p.unsub()
		p.unsub = nil
	}
	if p.sco != nil {
		p.sco.OnNodes = nil
	}
}
