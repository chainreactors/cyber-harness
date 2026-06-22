package config

import (
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

type RuntimeConfig struct {
	Provider      RuntimeProviderConfig
	Scanner       ScannerConfig
	Tools         ToolConfig
	IOA           *IOAConfig
	Logger        telemetry.Logger
	CLISkillPaths []string
}

type RuntimeProviderConfig struct {
	Enabled   bool
	Config    agent.ProviderConfig
	Fallbacks []agent.ProviderConfig
	Optional  bool
}

type ScannerConfig struct {
	CyberhubURL       string
	CyberhubKey       string
	CyberhubMode      string
	AIEnabled         bool
	EnableAllAISkills bool
	AITimeout         int
	VerifyMode        string
	Proxy             string
	FofaEmail         string
	FofaKey           string
	HunterToken       string
	HunterAPIKey      string
	ReconProxy        string
	ReconLimit        int
}

type ToolConfig struct {
	Enabled       bool
	BashTimeout   int
	TavilyKeys    string
	OptionalTools []string // optional tool groups to enable (e.g. "search", "browser")
}

type IOAConfig struct {
	URL           string
	NodeID        string
	NodeName      string
	Space         string
	RegisterTools bool
	AutoRegister  bool
	NodeMeta      map[string]any
}
