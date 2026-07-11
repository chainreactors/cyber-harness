//go:build cstx_native

package output

import (
	"encoding/json"

	"github.com/chainreactors/utils/cstx"
)

func CSTXTransform(tool string, data any) ([]json.RawMessage, error) {
	nodes, err := cstx.Parse(tool, data)
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(nodes))
	for _, n := range nodes {
		raw, err := json.Marshal(n)
		if err != nil {
			continue
		}
		out = append(out, raw)
	}
	return out, nil
}
