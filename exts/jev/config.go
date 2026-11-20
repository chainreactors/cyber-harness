package jev

import (
	"fmt"
	"strings"
	"time"

	jevapi "github.com/chainreactors/cyber/agent/provider/jev"
	"github.com/chainreactors/cyber/core/resource"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

const ConfigKey = "jev"

type Config struct {
	BaseURL            string `config:"base_url" json:"base_url"`
	APIKey             string `config:"api_key" json:"api_key" description:"TypeSafe API key (or TYPESAFE_API_KEY)"`
	Model              string `config:"model" json:"model"`
	Timeout            string `config:"timeout" json:"timeout" description:"Total request budget including retries"`
	Mode               string `config:"mode" json:"mode" description:"Optional accelerator: off (default), auto"`
	Learning           string `config:"learning" json:"learning" description:"auto (default) learns Claims and compiles; frozen only reuses qualified Reflexes"`
	Directory          string `config:"directory" json:"directory" description:"Claim/Reflex library and execution evidence directory; default .cyber/jev"`
	DeclarationEffort  string `config:"declaration_effort" json:"declaration_effort,omitempty" description:"Provider reasoning effort for background Claim/Compile; default none"`
	CompilationTimeout string `config:"compilation_timeout" json:"compilation_timeout,omitempty" description:"Optional total background compilation time; 0 (default) continues repair until accepted or canceled"`
}

func defaults(c Config) Config {
	if c.BaseURL == "" {
		c.BaseURL = "https://api.typesafe.ai"
	}
	if c.Learning == "" {
		c.Learning = "auto"
	}
	if c.Mode == "" {
		c.Mode = "off"
	}
	if c.Model == "" {
		c.Model = jevapi.DefaultModel
	}
	if c.Timeout == "" {
		c.Timeout = "10s"
	}
	if c.DeclarationEffort == "" {
		c.DeclarationEffort = "none"
	}
	if c.CompilationTimeout == "" {
		c.CompilationTimeout = "0"
	}
	return c
}

func (c Config) validate() error {
	c = defaults(c)
	if err := cfg.ValidateBaseURL(c.BaseURL); err != nil {
		return err
	}
	if c.Learning != "auto" && c.Learning != "frozen" {
		return fmt.Errorf("jev learning must be auto or frozen")
	}
	if c.Mode != "off" && c.Mode != "auto" {
		return fmt.Errorf("jev mode must be off or auto")
	}
	if d, err := time.ParseDuration(c.Timeout); err != nil || d <= 0 {
		return fmt.Errorf("jev timeout must be positive")
	}
	if d, err := time.ParseDuration(c.CompilationTimeout); err != nil || d < 0 {
		return fmt.Errorf("jev compilation_timeout must be zero or a positive duration")
	}
	return nil
}

var configSection = cfg.Section{
	Key: ConfigKey, New: func() any { c := defaults(Config{}); return &c }, Secrets: []string{"api_key"},
	Normalize: func(value any) error { config := value.(*Config); *config = defaults(*config); return nil },
	Validate:  func(v any) error { return v.(*Config).validate() },
	Environment: func(s cfg.Sources) (map[string]any, map[string]any, error) {
		if value, ok := s.LookupEnv("TYPESAFE_API_KEY"); ok && strings.TrimSpace(value) != "" {
			return nil, map[string]any{"api_key": value}, nil
		}
		return nil, nil, nil
	},
}

func Declare(resources *resource.Registry) error {
	_, err := resource.Add[cfg.Section](resources, configSection)
	if err != nil {
		return err
	}
	_, err = resource.Add[cfg.Connection](resources, cfg.Connection{Section: ConfigKey, Test: testConnection})
	return err
}
