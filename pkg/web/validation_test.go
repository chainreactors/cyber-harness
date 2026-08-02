package web

import (
	"strings"
	"testing"

	configpb "github.com/chainreactors/aiscan/pkg/types/config"
)

func TestValidateLLMConfigRejectsUnsupportedProvider(t *testing.T) {
	cfg := &configpb.LLMConfig{Providers: []*configpb.LLMProviderConfig{{
		Provider: "deepseek",
		Model:    "deepseek-chat",
	}}}
	if err := ValidateLLMConfig(cfg); err == nil || !strings.Contains(err.Error(), "use openai or anthropic") {
		t.Fatalf("ValidateLLMConfig() error = %v", err)
	}
}

func TestValidateTarget(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"192.168.1.1", false},
		{"10.0.0.1:8080", false},
		{"https://example.com", false},
		{"http://example.com/path", false},
		{"example.com", false},
		{"sub.example.com", false},

		{"", true},
		{"192.168.1.0/24", true},
		{"10.0.0.0/8", true},
		{"ftp://example.com", true},
		{"192.168.1.1, 192.168.1.2", true},
		{"192.168.1.1 192.168.1.2", true},
	}

	for _, tt := range tests {
		_, err := ValidateTarget(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateTarget(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
		}
	}
}

func TestValidateMode(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"quick", "quick", false},
		{"full", "full", false},
		{"", "quick", false},
		{"QUICK", "quick", false},
		{"invalid", "", true},
	}

	for _, tt := range tests {
		got, err := ValidateMode(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateMode(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
		}
		if got != tt.want && !tt.wantErr {
			t.Errorf("ValidateMode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
