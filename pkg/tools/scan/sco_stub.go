//go:build !cstx_native

package scan

import (
	"encoding/json"

	"github.com/chainreactors/aiscan/core/output"
)

func buildSCONodes(_ *output.Result) []json.RawMessage { return nil }
