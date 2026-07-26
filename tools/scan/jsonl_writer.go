package scan

import (
	"encoding/json"
	"strings"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/tools/scan/pipeline"
)

type scanJSONLWriter struct {
	w          *output.TimelineWriter
	scanUnsub  func()
	agentUnsub func()
}

func newScanJSONLWriter(path string, scanBus *eventbus.Bus[pipeline.Observation], agentBus *eventbus.Bus[aop.Event]) (*scanJSONLWriter, error) {
	tw, err := output.NewTimelineWriter(path)
	if err != nil {
		return nil, err
	}
	w := &scanJSONLWriter{w: tw}
	w.scanUnsub = scanBus.Subscribe(w.handleObservation)
	if agentBus != nil {
		w.agentUnsub = agentBus.Subscribe(w.handleAgentEvent)
	}
	return w, nil
}

func (w *scanJSONLWriter) Close() error {
	if w.scanUnsub != nil {
		w.scanUnsub()
		w.scanUnsub = nil
	}
	if w.agentUnsub != nil {
		w.agentUnsub()
		w.agentUnsub = nil
	}
	return w.w.Close()
}

func (w *scanJSONLWriter) WriteRecord(rec output.Record) {
	w.w.WriteRecord(rec)
}

func (w *scanJSONLWriter) handleObservation(obs pipeline.Observation) {
	if obs.Action != pipeline.ActionAccept {
		return
	}
	e, ok := obs.Event.(event)
	if !ok {
		return
	}
	for _, rec := range observationToRecords(e) {
		w.w.WriteRecord(rec)
	}
}

func (w *scanJSONLWriter) handleAgentEvent(event aop.Event) {
	raw, _ := json.Marshal(event)
	w.w.WriteRaw(raw)
}

func observationToRecords(e event) []output.Record {
	switch e.Kind {
	case eventTarget:
		return targetToRecords(e)
	case eventLoot:
		return lootToRecords(e)
	default:
		return nil
	}
}

func targetToRecords(e event) []output.Record {
	switch target := e.Target.(type) {
	case serviceTarget:
		if target.Result != nil {
			return []output.Record{output.NewRecord(output.TypeGogo, target.Result)}
		}
	case webProbeTarget:
		if reportableSprayResultForCapability(target.Result, target.Capability) && target.Result != nil {
			return []output.Record{output.NewRecord(output.TypeSpray, target.Result)}
		}
	}
	return nil
}

func lootToRecords(e event) []output.Record {
	if e.Loot == nil {
		return nil
	}
	return []output.Record{output.NewLootRecord(capabilityRecordType(e.Source), e.Loot)}
}

func capabilityRecordType(source string) output.RecordType {
	switch {
	case strings.HasPrefix(source, "gogo"):
		return output.TypeGogo
	case strings.HasPrefix(source, "spray"), source == capCoreWeb:
		return output.TypeSpray
	case strings.HasPrefix(source, "zombie"), source == capHTTPBasicAuth:
		return output.TypeZombie
	case strings.HasPrefix(source, "neutron"):
		return output.TypeNeutron
	default:
		return output.RecordType(source)
	}
}
