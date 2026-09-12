package app

import (
	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/core/capability"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
)

type Config struct {
	Capabilities  capability.Catalog
	Provider      ApplicationProviderConfig
	Scanner       ScannerConfig
	Tools         ToolConfig
	Logger        telemetry.Logger
	CLISkillPaths []string
	RecordFile    string
	SkipEngines   bool
}

type ApplicationProviderConfig struct {
	Enabled   bool
	Config    agent.ProviderConfig
	Fallbacks []agent.ProviderConfig
	Optional  bool
}

type ScannerConfig struct {
	CyberhubURL        string
	CyberhubKey        string
	CyberhubMode       string
	AIEnabled          bool
	VerifyMode         string
	Proxy              string
	FofaKey            string
	HunterAPIKey       string
	ReconProxy         string
	ReconLimit         int
	UncoverCredentials map[string]string
}

type ToolConfig struct {
	Enabled           bool
	RunnerMode        bool
	BashTimeout       int
	TavilyKeys        string
	PlaywrightSession string
	OptionalTools     []string // optional tool groups to enable
	MitmCapture       *bool    // nil defaults to capture; false keeps routing without interception
	TrafficStorage    cfg.TrafficOptions
}
