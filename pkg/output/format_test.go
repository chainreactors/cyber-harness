package output

import (
	"testing"
)

func TestStripANSIPrivateModeSequences(t *testing.T) {
	input := "\x1b[?9001h\x1b[?1004h\x1b[?25lchat_pass \x1b[?25h"
	if got := StripANSI(input); got != "chat_pass " {
		t.Fatalf("StripANSI = %q, want %q", got, "chat_pass ")
	}
}
