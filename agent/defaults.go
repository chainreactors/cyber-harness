package agent

import "github.com/chainreactors/cyber/core/truncate"

const (
	DefaultMaxResultSize         = truncate.DefaultMaxBytes
	DefaultMaxRetries            = 9
	DefaultMaxTokens             = 16384
	ContextSafetyTokens          = 4096
	DefaultCompactionReserve     = 16384
	DefaultKeepRecentTokens      = 20000
	DefaultTokenBudgetWarningPct = 80
	DefaultInboxCapacity         = 64
	SubInboxCapacity             = 64
	DefaultMaxParallelTools      = 16
)
