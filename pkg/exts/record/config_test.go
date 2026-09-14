package record

import (
	"strings"
	"testing"
)

func TestMaxConcurrentFromEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		present bool
		want    int
		wantErr string
	}{
		{name: "unset", want: defaultMaxConcurrent},
		{name: "blank", value: "  ", present: true, want: defaultMaxConcurrent},
		{name: "configured", value: " 6 ", present: true, want: 6},
		{name: "not integer", value: "many", present: true, wantErr: "must be an integer"},
		{name: "zero", value: "0", present: true, wantErr: "must be between"},
		{name: "above limit", value: "17", present: true, wantErr: "must be between"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := maxConcurrentFromEnvironment(func(name string) (string, bool) {
				if name != maxConcurrentEnv || !tt.present {
					return "", false
				}
				return tt.value, true
			})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if got != tt.want {
					t.Fatalf("max concurrent = %d, want %d", got, tt.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}
