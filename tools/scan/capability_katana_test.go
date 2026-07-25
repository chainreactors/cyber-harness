//go:build full

package scan

import (
	"context"
	"testing"
	"time"
)

func TestKatanaProfileExtender(t *testing.T) {
	quick, err := profileForMode("quick")
	if err != nil {
		t.Fatalf("quick profile error: %v", err)
	}
	if !quick.Enabled(capKatanaCrawl) {
		t.Fatal("quick profile should enable katana_crawl")
	}
	if quick.Enabled(capKatanaDeep) {
		t.Fatal("quick profile should not enable katana_deep")
	}

	full, err := profileForMode("full")
	if err != nil {
		t.Fatalf("full profile error: %v", err)
	}
	if !full.Enabled(capKatanaCrawl) {
		t.Fatal("full profile should enable katana_crawl")
	}
	if !full.Enabled(capKatanaDeep) {
		t.Fatal("full profile should enable katana_deep")
	}
}

func TestRunKatanaCrawlEmitsTargets(t *testing.T) {
	cmd := &Command{}
	wt := newWebTarget("", "https://www.example.com", "")
	e := targetEvent(capSprayCheck, "", wt)

	var emitted []event
	emit := func(ev event) {
		emitted = append(emitted, ev)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runKatanaCrawl(ctx, cmd, e, 1, false, emit)

	targets := 0
	for _, ev := range emitted {
		if ev.Kind == eventTarget {
			targets++
			if wTarget, ok := ev.Target.(webTarget); ok {
				t.Logf("  discovered: %s", wTarget.URL)
			}
		}
	}
	t.Logf("katana discovered %d web targets from example.com (depth=1)", targets)
}
