package engine

import (
	"testing"
	"time"
)

func TestBuildSprayOptionAppliesDebugAndRuntimeOptions(t *testing.T) {
	opt := buildSprayOption(SprayCheckOptions{
		Host:         "vhost.example",
		Dictionaries: []string{"paths.txt"},
		Rules:        []string{"rules.txt"},
		Word:         "admin",
		DefaultDict:  true,
		Advance:      true,
		Crawl:        true,
		CrawlDepth:   1,
		Finger:       true,
		Threads:      7,
		Timeout:      9,
		Debug:        true,
	})

	if !opt.Debug {
		t.Fatal("debug = false, want true")
	}
	if opt.Quiet {
		t.Fatal("quiet = true, want false in debug mode")
	}
	if opt.Threads != 7 || opt.Timeout != 9 || opt.Host != "vhost.example" {
		t.Fatalf("runtime options = threads:%d timeout:%d host:%q", opt.Threads, opt.Timeout, opt.Host)
	}
	if !opt.CrawlPlugin || opt.CrawlDepth != 1 || !opt.Finger || !opt.DefaultDict || !opt.Advance {
		t.Fatalf("plugin options = %#v", opt.PluginOptions)
	}
	if len(opt.Dictionaries) != 1 || opt.Dictionaries[0] != "paths.txt" || len(opt.Rules) != 1 || opt.Rules[0] != "rules.txt" || opt.Word != "admin" {
		t.Fatalf("word options = dicts:%#v rules:%#v word:%q", opt.Dictionaries, opt.Rules, opt.Word)
	}
}

func TestDefaultSprayInvocationTimeoutBoundsCrawl(t *testing.T) {
	got := defaultSprayInvocationTimeout(SprayCheckOptions{Timeout: 5, Crawl: true, CrawlDepth: 2})
	if got != 80*time.Second {
		t.Fatalf("crawl invocation timeout = %s, want 80s", got)
	}
}
