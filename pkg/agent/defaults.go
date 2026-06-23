package agent

import "github.com/chainreactors/aiscan/pkg/agent/truncate"

const (
	DefaultMaxResultSize         = truncate.DefaultMaxBytes
	DefaultMaxRetries            = 9
	DefaultTokenBudgetWarningPct = 80
	DefaultInboxCapacity         = 64
	SubInboxCapacity             = 16
	DefaultMaxParallelTools      = 16
)
