package runner

import (
	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/ioa/protocols"
)

type ApplicationConfig struct {
	Provider      ApplicationProviderConfig
	Scanner       ScannerConfig
	Tools         ToolConfig
	IOA           *IOAConfig
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
	BashTimeout       int
	TavilyKeys        string
	PlaywrightSession string
	OptionalTools     []string // optional tool groups to enable
	MitmCapture       bool     // record tool traffic through the MITM hub (default true); false = pure proxy routing
}

type IOAConfig struct {
	URL           string
	NodeID        string
	NodeName      string
	Space         string
	RegisterTools bool
	AutoRegister  bool
	NodeMeta      map[string]any
	Identity      protocols.Identity
}
