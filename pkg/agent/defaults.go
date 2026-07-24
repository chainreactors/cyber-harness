package agent

import "github.com/chainreactors/aiscan/pkg/agent/truncate"

const (
	DefaultMaxResultSize         = truncate.DefaultMaxBytes
	DefaultMaxRetries            = 9
	DefaultMaxTokens             = 16384
	ContextSafetyTokens          = 4096
	DefaultCompactionReserve     = 16384
	DefaultKeepRecentTokens      = 20000
	DefaultTokenBudgetWarningPct = 80
	DefaultInboxCapacity         = 64
	SubInboxCapacity             = 16
	DefaultMaxParallelTools      = 16
)
