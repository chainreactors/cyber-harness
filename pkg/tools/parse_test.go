package tools

import (
	"reflect"
	"testing"
)

func TestSplitCommandLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "simple",
			in:   "scan -i 192.168.1.1",
			want: []string{"scan", "-i", "192.168.1.1"},
		},
		{
			name: "quoted spaces",
			in:   `spray -u "http://example.com/a b" --finger`,
			want: []string{"spray", "-u", "http://example.com/a b", "--finger"},
		},
		{
			name: "windows path",
			in:   `scan -l C:\tmp\targets.txt`,
			want: []string{"scan", "-l", `C:\tmp\targets.txt`},
		},
		{
			name: "escaped space",
			in:   `scan -l C:\scan\my\ targets.txt`,
			want: []string{"scan", "-l", `C:\scan\my targets.txt`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitCommandLine(tt.in)
			if err != nil {
				t.Fatalf("splitCommandLine() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("splitCommandLine() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSplitCommandLineUnterminatedQuote(t *testing.T) {
	if _, err := splitCommandLine(`scan -i "unterminated`); err == nil {
		t.Fatal("expected error")
	}
}
