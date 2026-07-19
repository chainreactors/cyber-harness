package provider

import "testing"

func TestStructuredOutputModeFor(t *testing.T) {
	tests := []struct {
		name string
		cfg  ProviderConfig
		want StructuredOutputMode
	}{
		{
			name: "official deepseek",
			cfg:  ProviderConfig{BaseURL: "https://api.deepseek.com"},
			want: StructuredOutputJSONObject,
		},
		{
			name: "openai compatible default",
			cfg:  ProviderConfig{BaseURL: "https://api.openai.com/v1"},
			want: StructuredOutputJSONSchema,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StructuredOutputModeFor(tt.cfg); got != tt.want {
				t.Fatalf("StructuredOutputModeFor() = %q, want %q", got, tt.want)
			}
		})
	}
}
