package scan

import (
	"strings"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/tools/scan/pipeline"
	"github.com/chainreactors/utils/parsers"
	sdktypes "github.com/chainreactors/sdk/pkg/types"
)

type scanJSONLWriter struct {
	w          *output.TimelineWriter
	scanUnsub  func()
	agentUnsub func()
}

func newScanJSONLWriter(path string, scanBus *eventbus.Bus[pipeline.Observation], agentBus *eventbus.Bus[agent.Event]) (*scanJSONLWriter, error) {
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

func (w *scanJSONLWriter) handleAgentEvent(event agent.Event) {
	w.w.WriteRecord(output.NewRecord(output.TypeAgent, event))
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

func ObservationToRecord(obs pipeline.Observation) *output.Record {
	if obs.Action != pipeline.ActionAccept {
		return nil
	}
	e, ok := obs.Event.(event)
	if !ok {
		return nil
	}
	records := observationToRecords(e)
	if len(records) == 0 {
		return nil
	}
	return &records[0]
}

type ServiceResult = parsers.GOGOResult
type SprayResult = parsers.SprayResult
type ZombieResult = parsers.ZombieResult
type VulnResult = sdktypes.VulnResult
