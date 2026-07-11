//go:build cstx_native

package scan

import (
	"encoding/json"
	"testing"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/tools/scan/pipeline"
	"github.com/chainreactors/utils/parsers"
)

func TestStructuredResultContainsSCONodes(t *testing.T) {
	bus := eventbus.New[pipeline.Observation]()
	coll := newCollector([]string{"192.168.1.0/24"}, nil, false, false)
	subscribePipeline(bus, coll, false)

	// Simulate gogo finding a service
	gogoResult := &parsers.GOGOResult{
		Ip: "192.168.1.1", Port: "80", Protocol: "tcp",
		Status: "200", Uri: "http://192.168.1.1/",
		Title: "Test", Midware: "nginx",
		Frameworks: parsers.Frameworks{
			"nginx": &parsers.Framework{Name: "nginx"},
		},
	}
	coll.Observe(pipelineEvent{
		Action: pipeline.ActionAccept,
		Event:  targetEvent(capGogoPortscan, "", newServiceTarget("", gogoResult)),
	})

	// Simulate spray finding a web page
	sprayResult := &parsers.SprayResult{
		UrlString: "http://192.168.1.1/", Title: "Test",
		Status: 200, Host: "192.168.1.1",
		BodyLength: 1024, ContentType: "text/html",
	}
	coll.Observe(pipelineEvent{
		Action: pipeline.ActionAccept,
		Event:  targetEvent(capSprayCheck, "", newWebProbeTarget("", capSprayCheck, "", sprayResult)),
	})

	coll.Finish()
	result := coll.StructuredResult()

	// Verify nodes exist
	if len(result.Nodes) == 0 {
		t.Fatal("StructuredResult().Nodes is empty — SCO conversion did not run")
	}

	// Parse nodes back and check types
	types := make(map[string]int)
	for _, raw := range result.Nodes {
		var header struct {
			Type string `json:"cstx_type"`
			ID   string `json:"cstx_id"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			t.Errorf("failed to unmarshal node: %v", err)
			continue
		}
		types[header.Type]++
		t.Logf("  %s  %s", header.Type, header.ID)
	}

	// Should have at least ip, port, app from gogo + url, app from spray
	for _, expect := range []string{"ip", "port", "app"} {
		if types[expect] == 0 {
			t.Errorf("missing SCO node type: %s", expect)
		}
	}

	t.Logf("total SCO nodes: %d, types: %v", len(result.Nodes), types)

	// Verify JSON serialization round-trip
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}
	if len(resultJSON) == 0 {
		t.Fatal("empty result JSON")
	}
	t.Logf("Result JSON size: %d bytes", len(resultJSON))
}
