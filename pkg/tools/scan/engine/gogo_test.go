package engine

import "testing"

func TestBuildGogoRunnerOptionAppliesVersionAndExploit(t *testing.T) {
	opt := buildGogoRunnerOption(GogoScanOptions{
		Timeout:      7,
		VersionLevel: 1,
		Exploit:      "auto",
	})

	if opt.VersionLevel != 1 {
		t.Fatalf("version level = %d, want 1", opt.VersionLevel)
	}
	if opt.Exploit != "auto" {
		t.Fatalf("exploit = %q, want auto", opt.Exploit)
	}
	if opt.Delay != 7 || opt.HttpsDelay != 7 {
		t.Fatalf("delay = %d/%d, want 7/7", opt.Delay, opt.HttpsDelay)
	}
}
