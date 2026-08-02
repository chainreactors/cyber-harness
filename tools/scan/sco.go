package scan

import (
	"encoding/json"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/libcstx/go"
)

func buildSCONodes(result *output.ScanResult) []json.RawMessage {
	var allNodes []cstx.SCONode

	if len(result.GOGO) > 0 {
		if nodes, err := cstx.Parse("gogo", result.GOGO); err == nil {
			allNodes = append(allNodes, nodes...)
		}
	}
	if len(result.Spray) > 0 {
		if nodes, err := cstx.Parse("spray", result.Spray); err == nil {
			allNodes = append(allNodes, nodes...)
		}
	}

	seen := make(map[string]struct{}, len(allNodes))
	out := make([]json.RawMessage, 0, len(allNodes))
	for _, n := range allNodes {
		id := n.CstxID()
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if raw, err := json.Marshal(n); err == nil {
			out = append(out, raw)
		}
	}
	return out
}
