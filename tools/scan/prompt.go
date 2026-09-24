package scan

import (
	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/utils/parsers"
)

const (
	// ScannerSystemTarget is the system prompt target of a single-scanner
	// worker session (e.g. `aiscan gogo --ai`).
	ScannerSystemTarget prompt.Target = "scanner.system"
	VerifySystemTarget  prompt.Target = "scanner.verify.system"
	VerifyRequestTarget prompt.Target = "scanner.verify.request"
	SniperSystemTarget  prompt.Target = "scanner.sniper.system"
	SniperRequestTarget prompt.Target = "scanner.sniper.request"
)

// WorkerPromptPayload is the scanner-owned input rendered by its prompt
// contributions. The generic prompt point treats it as an opaque payload.
type WorkerPromptPayload struct {
	Loot parsers.Loot
}
