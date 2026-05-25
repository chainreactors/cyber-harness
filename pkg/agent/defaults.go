package agent

import "time"

const (
	DefaultMaxResultSize         = 50 * 1024
	DefaultInboxIdlePollInterval = time.Second
	DefaultMaxRetries            = 2
	DefaultTokenBudgetWarningPct = 80
	DefaultSkillMaxTokens        = 1600
)
