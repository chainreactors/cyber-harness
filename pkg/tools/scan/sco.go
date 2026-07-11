//go:build cstx_native

package scan

import (
	"encoding/json"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/utils/cstx"
)

func buildSCONodes(result *output.Result) []json.RawMessage {
	var allNodes []cstx.SCONode

	if len(result.Services) > 0 {
		if nodes, err := cstx.Parse("gogo", result.Services); err == nil {
			allNodes = append(allNodes, nodes...)
		}
	}
	if len(result.WebProbes) > 0 {
		if nodes, err := cstx.Parse("spray", result.WebProbes); err == nil {
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
