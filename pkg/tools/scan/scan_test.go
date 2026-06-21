package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/chainreactors/aiscan/pkg/tools/scan/pipeline"
	"github.com/chainreactors/fingers/common"
	"github.com/chainreactors/logs"
	"github.com/chainreactors/neutron/operators"
	neutronhttp "github.com/chainreactors/neutron/protocols/http"
	"github.com/chainreactors/neutron/templates"
	"github.com/chainreactors/parsers"
	sdkgogo "github.com/chainreactors/sdk/gogo"
	sdkneutron "github.com/chainreactors/sdk/neutron"
	"github.com/chainreactors/sdk/pkg/association"
	sdktypes "github.com/chainreactors/sdk/pkg/types"
	"github.com/chainreactors/sdk/spray"
	sdkzombie "github.com/chainreactors/sdk/zombie"
)

func newTestPipeline(t *testing.T, ctx context.Context, caps []pipeline.Capability, coll *collector, debug bool) *pipeline.Pipeline {
	t.Helper()
	bus := eventbus.New[pipeline.Observation]()
	subscribePipeline(bus, coll, debug)
	p, err := pipeline.New(ctx, pipeline.Config{
		Capabilities: caps,
		Bus:          bus,
	})
	if err != nil {
		t.Fatalf("pipeline.New() error = %v", err)
	}
	return p
}

func testSeeds(events ...event) []pipeline.Event {
	return seedsToEvents(events)
}

func TestScanRunsWithOnlySprayStage(t *testing.T) {
	sprayEng, _ := spray.NewEngine(nil)
	cmd := New(&engine.Set{Spray: sprayEng})
	commands.Output.Reset(nil)
	err := cmd.Execute(context.Background(), []string{"-i", "http://127.0.0.1:1", "--mode", "quick", "--timeout", "1"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := commands.Output.Captured()
	if !strings.Contains(out, "[summary] completed") {
		t.Fatalf("output missing summary: %q", out)
	}
}

func TestScanProfilesAssembleCapabilities(t *testing.T) {
	quick, err := profileForMode("quick")
	if err != nil {
		t.Fatalf("quick profile error = %v", err)
	}
	for _, name := range []string{capGogoPortscan, capSprayCheck, capCoreWeb, capSprayCrawl, capZombieWeakpass, capNeutronPOC} {
		if !quick.Enabled(name) {
			t.Fatalf("quick profile missing %s", name)
		}
	}
	if quick.CrawlDepth != 2 {
		t.Fatalf("quick crawl depth = %d, want 2", quick.CrawlDepth)
	}
	for _, name := range []string{capSprayPlugins, capSprayBrute} {
		if quick.Enabled(name) {
			t.Fatalf("quick profile should not enable %s", name)
		}
	}

	full, err := profileForMode("full")
	if err != nil {
		t.Fatalf("full profile error = %v", err)
	}
	for _, name := range []string{capGogoPortscan, capSprayCheck, capCoreWeb, capSprayPlugins, capSprayCrawl, capSprayBrute, capZombieWeakpass, capNeutronPOC} {
		if !full.Enabled(name) {
			t.Fatalf("full profile missing %s", name)
		}
	}
	if full.CrawlDepth != 2 {
		t.Fatalf("full crawl depth = %d, want 2", full.CrawlDepth)
	}
}

func TestScanOptionsResolveCredentialFlags(t *testing.T) {
	flags := flags{
		Users:     []string{"root", "admin"},
		Passwords: []string{"toor", "admin123"},
	}
	opts := resolveScanOptions(flags)
	if !reflect.DeepEqual(opts.Credentials.Users, flags.Users) {
		t.Fatalf("credential users = %#v, want %#v", opts.Credentials.Users, flags.Users)
	}
	if !reflect.DeepEqual(opts.Credentials.Passwords, flags.Passwords) {
		t.Fatalf("credential passwords = %#v, want %#v", opts.Credentials.Passwords, flags.Passwords)
	}
	if !opts.hasWeakpassOverrides() {
		t.Fatal("expected weakpass overrides")
	}
	flags.Users[0] = "mutated"
	flags.Passwords[0] = "mutated"
	if opts.Credentials.Users[0] != "root" || opts.Credentials.Passwords[0] != "toor" {
		t.Fatalf("scan options aliases flags slices: %#v", opts.Credentials)
	}
}

func TestScanOptionsResolveDiscoveryFlags(t *testing.T) {
	opts := resolveScanOptions(flags{Mode: scanModeQuick})
	if opts.Discovery.Ports != scanQuickDefaultPorts || opts.Discovery.Version != scanGogoVersionLevel || opts.Discovery.Exploit != scanGogoExploitMode || opts.hasDiscoveryOverrides() {
		t.Fatalf("quick discovery defaults = %#v", opts.Discovery)
	}

	opts = resolveScanOptions(flags{Mode: scanModeFull})
	if opts.Discovery.Ports != scanFullDefaultPorts || opts.Discovery.Version != scanGogoVersionLevel || opts.Discovery.Exploit != scanGogoExploitMode || opts.hasDiscoveryOverrides() {
		t.Fatalf("full discovery defaults = %#v", opts.Discovery)
	}

	flagValues := flags{
		Mode:    scanModeFull,
		Ports:   "top100",
		Port:    "80,443",
		Threads: 77, // set internally by derivePerInvocationThreads
		Timeout: 6,
	}
	opts = resolveScanOptions(flagValues)
	if opts.Discovery.Ports != "80,443" {
		t.Fatalf("discovery ports = %q, want --port override", opts.Discovery.Ports)
	}
	if opts.Discovery.Threads != 77 || opts.Discovery.Timeout != 6 {
		t.Fatalf("discovery options = %#v", opts.Discovery)
	}
	if !opts.hasDiscoveryOverrides() {
		t.Fatal("expected discovery overrides")
	}

	opts = resolveScanOptions(flags{Mode: scanModeFull, Ports: "top10", Threads: 5, Timeout: 9})
	if opts.Discovery.Ports != "top10" || opts.Discovery.Timeout != 9 {
		t.Fatalf("discovery fallback options = %#v", opts.Discovery)
	}
	if !opts.hasDiscoveryOverrides() {
		t.Fatal("--ports should count as explicit discovery override")
	}
}

func TestScanUsageHidesDeprecatedAliases(t *testing.T) {
	usage := Usage()
	if strings.Contains(usage, "--verify-timeout") {
		t.Fatal("usage should not advertise deprecated --verify-timeout")
	}
	if strings.Contains(usage, "--port        ") || strings.Contains(usage, "--port top100") {
		t.Fatal("usage should not advertise deprecated --port alias")
	}
	if strings.Contains(usage, "--ai") {
		t.Fatal("usage should not advertise removed --ai alias")
	}
}

func TestScanRejectsRemovedAIFlag(t *testing.T) {
	cmd := New(&engine.Set{})
	commands.Output.Reset(nil)
	err := cmd.Execute(context.Background(), []string{
		"-i", "http://127.0.0.1",
		"--ai",
		"--no-color",
	})
	if err == nil {
		t.Fatal("Execute() with removed --ai flag should fail")
	}
	if !strings.Contains(err.Error(), "unknown flag") || !strings.Contains(err.Error(), "ai") {
		t.Fatalf("error = %v, want unknown ai flag", err)
	}
}

func TestScanAcceptsDeprecatedCompatibilityFlags(t *testing.T) {
	cmd := New(&engine.Set{})
	commands.Output.Reset(nil)
	err := cmd.Execute(context.Background(), []string{
		"-i", "http://127.0.0.1",
		"--verify-timeout", "1",
		"--port", "top100",
		"--no-color",
	})
	if err != nil {
		t.Fatalf("Execute() with deprecated compatibility flags error = %v", err)
	}
}

func TestScanOptionsResolveWebStrategyFlags(t *testing.T) {
	flags := flags{
		Dictionaries: []string{"paths.txt", "api.txt"},
		Rules:        []string{"rules.txt"},
		Word:         "admin{?ld#2}",
		DefaultDict:  true,
		Advance:      true,
	}
	opts := resolveScanOptions(flags)
	if !reflect.DeepEqual(opts.Web.Dictionaries, flags.Dictionaries) {
		t.Fatalf("web dictionaries = %#v, want %#v", opts.Web.Dictionaries, flags.Dictionaries)
	}
	if !reflect.DeepEqual(opts.Web.Rules, flags.Rules) {
		t.Fatalf("web rules = %#v, want %#v", opts.Web.Rules, flags.Rules)
	}
	if opts.Web.Word != flags.Word || !opts.Web.DefaultDict || !opts.Web.Advance {
		t.Fatalf("web options = %#v", opts.Web)
	}
	if !opts.hasWebOverrides() {
		t.Fatal("expected web overrides")
	}
	flags.Dictionaries[0] = "mutated"
	flags.Rules[0] = "mutated"
	if opts.Web.Dictionaries[0] != "paths.txt" || opts.Web.Rules[0] != "rules.txt" {
		t.Fatalf("scan web options alias flags slices: %#v", opts.Web)
	}
}

func TestScanWarnsWhenDiscoveryFlagsCannotAffectGogoCapability(t *testing.T) {
	var logBuf bytes.Buffer
	cmd := New(&engine.Set{}, WithLogger(telemetry.NewLogger(telemetry.LogConfig{Output: &logBuf})))
	profile := profile{Capabilities: capabilitySet(capGogoPortscan)}
	caps := cmd.buildCapabilities(flags{}, scanOptions{Discovery: discoveryOptions{Ports: "top100", Explicit: true}}, profile)
	if len(caps) != 0 {
		t.Fatalf("capabilities = %d, want 0 without gogo engine", len(caps))
	}
	if !strings.Contains(logBuf.String(), "option=port status=ignored reason=engine_unavailable") {
		t.Fatalf("warning log missing discovery ignore message: %q", logBuf.String())
	}
}

func TestScanWarnsWhenCredentialFlagsCannotAffectWeakpassCapability(t *testing.T) {
	var logBuf bytes.Buffer
	cmd := New(&engine.Set{}, WithLogger(telemetry.NewLogger(telemetry.LogConfig{Output: &logBuf})))
	profile := profile{Capabilities: capabilitySet(capZombieWeakpass)}
	caps := cmd.buildCapabilities(flags{}, scanOptions{Credentials: credentialOptions{Users: []string{"root"}}}, profile)
	if len(caps) != 0 {
		t.Fatalf("capabilities = %d, want 0 without zombie engine", len(caps))
	}
	if !strings.Contains(logBuf.String(), "option=user,pwd status=ignored reason=engine_unavailable") {
		t.Fatalf("warning log missing credential ignore message: %q", logBuf.String())
	}
}

func TestScanWarnsWhenWebFlagsCannotAffectSprayCapability(t *testing.T) {
	var logBuf bytes.Buffer
	cmd := New(&engine.Set{}, WithLogger(telemetry.NewLogger(telemetry.LogConfig{Output: &logBuf})))
	profile := profile{Capabilities: capabilitySet(capSprayPlugins)}
	caps := cmd.buildCapabilities(flags{}, scanOptions{Web: webOptions{Dictionaries: []string{"paths.txt"}}}, profile)
	if len(caps) != 0 {
		t.Fatalf("capabilities = %d, want 0 without spray engine", len(caps))
	}
	if !strings.Contains(logBuf.String(), "option=dict,rule,word,default-dict,advance status=ignored reason=engine_unavailable") {
		t.Fatalf("warning log missing web ignore message: %q", logBuf.String())
	}
}

func TestSprayCapabilityAppliesWebStrategyOptions(t *testing.T) {
	var got engine.SprayCheckOptions
	web := webOptions{
		Dictionaries: []string{"paths.txt"},
		Rules:        []string{"rules.txt"},
		Word:         "admin{?ld#2}",
		DefaultDict:  true,
		Advance:      true,
	}
	cmd := &Command{engines: &engine.Set{Capacity: distributeCapacity(1000)}}
	cap := sprayCapability(cmd, flags{SprayThreads: 7, Timeout: 9}, web, capSprayPlugins, webSources(), engine.SprayCheckOptions{CommonPlugin: true, BakPlugin: true, ActivePlugin: true, Finger: true}, func(_ context.Context, f flags, gotWeb webOptions, input target, source string, opts engine.SprayCheckOptions, emit func(event)) {
		target, ok := input.(webTarget)
		if !ok {
			t.Fatalf("input = %#v, want webTarget", input)
		}
		opts.URLs = []string{target.URL}
		opts.Threads = f.SprayThreads
		opts.Timeout = f.Timeout
		opts.Dictionaries = gotWeb.Dictionaries
		opts.Rules = gotWeb.Rules
		opts.Word = gotWeb.Word
		opts.DefaultDict = gotWeb.DefaultDict
		opts.Advance = gotWeb.Advance
		got = opts
		emit(targetEvent(source, target.Raw, newWebProbeTarget(target.Raw, source, "", &parsers.SprayResult{IsValid: true, UrlString: target.URL, Status: 200, Distance: 1})))
	})

	var emitted []event
	cap.Run(context.Background(), targetEvent("test", "raw", newWebTarget("raw", "http://127.0.0.1", "")), func(pe pipeline.Event) {
		if e, ok := pe.(event); ok {
			emitted = append(emitted, e)
		}
	})

	if !reflect.DeepEqual(got.Dictionaries, web.Dictionaries) || !reflect.DeepEqual(got.Rules, web.Rules) {
		t.Fatalf("spray dictionaries/rules = %#v/%#v", got.Dictionaries, got.Rules)
	}
	if got.Word != web.Word || !got.DefaultDict || !got.Advance {
		t.Fatalf("spray web strategy options = %#v", got)
	}
	if got.Threads != 7 || got.Timeout != 9 || !got.CommonPlugin || !got.BakPlugin || !got.ActivePlugin || !got.Finger {
		t.Fatalf("spray base options = %#v", got)
	}
	if len(emitted) != 1 || emitted[0].Target == nil {
		t.Fatalf("emitted = %#v, want one target event", emitted)
	}
	if emitted[0].Source != capSprayPlugins {
		t.Fatalf("emitted source = %q, want %q", emitted[0].Source, capSprayPlugins)
	}
}

func TestApplyWebStrategyOptionsEnablesReconAndPreservesCapabilityDefaults(t *testing.T) {
	web := webOptions{
		Dictionaries: []string{"paths.txt"},
		Rules:        []string{"rules.txt"},
		Word:         "admin",
	}
	opts := applyWebStrategyOptions(flags{SprayThreads: 7, Timeout: 9}, web, engine.SprayCheckOptions{DefaultDict: true, BakPlugin: true})
	if !opts.ReconPlugin || !opts.DefaultDict || !opts.BakPlugin {
		t.Fatalf("spray options should preserve capability defaults and enable recon: %#v", opts)
	}
	if opts.FuzzuliPlugin {
		t.Fatalf("backup capability should not enable fuzzuli by default: %#v", opts)
	}
	if opts.Threads != 7 || opts.Timeout != 9 || opts.Word != "admin" {
		t.Fatalf("spray runtime options = %#v", opts)
	}
	if !reflect.DeepEqual(opts.Dictionaries, web.Dictionaries) || !reflect.DeepEqual(opts.Rules, web.Rules) {
		t.Fatalf("spray dictionaries/rules = %#v/%#v", opts.Dictionaries, opts.Rules)
	}
}

func TestWebTargetScopeUsesAssetHost(t *testing.T) {
	got := webTargetScope(newWebTarget("", "http://127.0.0.1:4200/", "app.local"))
	want := []string{"127.0.0.1:4200", "app.local", "app.local:4200"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scope = %#v, want %#v", got, want)
	}
}

func TestSprayResultScopeRejectsExternalURLs(t *testing.T) {
	base := "http://127.0.0.1:4200/"
	if got := sanitizeSprayResultScope(base, &parsers.SprayResult{UrlString: "https://github.com/trending"}); got != nil {
		t.Fatalf("external result kept: %#v", got)
	}
	got := sanitizeSprayResultScope(base, &parsers.SprayResult{
		UrlString:   "http://127.0.0.1:4200/assets/app.js",
		RedirectURL: "https://github.com/login",
		IsValid:     true,
		Status:      200,
		BodyLength:  12,
		ContentType: "text/javascript",
		ReqDepth:    1,
	})
	if got == nil {
		t.Fatal("same asset result was dropped")
	}
	if got.RedirectURL != "" {
		t.Fatalf("external redirect was not cleared: %#v", got.RedirectURL)
	}
}

func TestScanBuildCapabilitiesUsesCapacityDrivenWorkers(t *testing.T) {
	gogoEng, _ := sdkgogo.NewEngine(nil)
	sprayEng, _ := spray.NewEngine(nil)
	cmd := New(&engine.Set{
		Gogo:  gogoEng,
		Spray: sprayEng,
	})
	profile := profile{Capabilities: capabilitySet(
		capGogoPortscan,
		capSprayCheck,
		capSprayPlugins,
		capSprayCrawl,
		capSprayBrute,
	)}

	// --thread 1000 distributes: gogo=800, spray=100
	// per-invocation auto-derived: gogo=500, spray=20
	f := flags{Thread: 1000}
	caps := cmd.buildCapabilities(f, scanOptions{}, profile)
	workers := make(map[string]int, len(caps))
	for _, cap := range caps {
		workers[cap.Name] = cap.Worker
	}

	// gogo: 800/500 = 1, spray: 100/20 = 5
	want := map[string]int{
		capGogoPortscan: 1,
		capSprayCheck:   5,
		capSprayPlugins: 5,
		capSprayCrawl:   5,
		capSprayBrute:   5,
	}
	for name, wantWorkers := range want {
		if got := workers[name]; got != wantWorkers {
			t.Fatalf("%s workers = %d, want %d", name, got, wantWorkers)
		}
	}
}

func TestScanBuildCapabilitiesAdaptsToHighThread(t *testing.T) {
	gogoEng, _ := sdkgogo.NewEngine(nil)
	sprayEng, _ := spray.NewEngine(nil)
	cmd := New(&engine.Set{
		Gogo:  gogoEng,
		Spray: sprayEng,
	})
	profile := profile{Capabilities: capabilitySet(capGogoPortscan, capSprayCheck)}

	// --thread 2000 distributes: gogo=1600, spray=200
	// per-invocation auto-derived: gogo=500, spray=20
	f := flags{Thread: 2000}
	caps := cmd.buildCapabilities(f, scanOptions{}, profile)
	workers := make(map[string]int, len(caps))
	for _, cap := range caps {
		workers[cap.Name] = cap.Worker
	}

	// gogo: 1600/500 = 3, spray: 200/20 = 10
	if got := workers[capGogoPortscan]; got != 3 {
		t.Fatalf("gogo workers = %d, want 3", got)
	}
	if got := workers[capSprayCheck]; got != 10 {
		t.Fatalf("spray_check workers = %d, want 10", got)
	}
	if cmd.engines.Capacity.Gogo != 1600 {
		t.Fatalf("gogo capacity = %d, want 1600", cmd.engines.Capacity.Gogo)
	}
}

func TestScanBuildCapabilitiesLowThreadCapsPerInvocation(t *testing.T) {
	gogoEng, _ := sdkgogo.NewEngine(nil)
	sprayEng, _ := spray.NewEngine(nil)
	cmd := New(&engine.Set{
		Gogo:  gogoEng,
		Spray: sprayEng,
	})
	profile := profile{Capabilities: capabilitySet(capGogoPortscan, capSprayCheck)}

	// --thread 100 distributes: gogo=80, spray=10
	// per-invocation capped: gogo=min(500,80)=80, spray=min(20,10)=10
	f := flags{Thread: 100}
	caps := cmd.buildCapabilities(f, scanOptions{}, profile)
	workers := make(map[string]int, len(caps))
	for _, cap := range caps {
		workers[cap.Name] = cap.Worker
	}

	// gogo: 80/80 = 1, spray: 10/10 = 1
	if got := workers[capGogoPortscan]; got != 1 {
		t.Fatalf("gogo workers = %d, want 1", got)
	}
	if got := workers[capSprayCheck]; got != 1 {
		t.Fatalf("spray_check workers = %d, want 1", got)
	}
}

func TestScanSeedTargetsFromInputs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		kinds []targetKind
	}{
		{
			name:  "url",
			input: "http://example.com",
			kinds: []targetKind{targetWeb},
		},
		{
			name:  "hostport web",
			input: "127.0.0.1:8080",
			kinds: []targetKind{targetScan, targetWeb},
		},
		{
			name:  "cidr",
			input: "192.168.1.0/24",
			kinds: []targetKind{targetScan},
		},
		{
			name:  "service url",
			input: "ssh://root@127.0.0.1:22",
			kinds: []targetKind{targetWeakpass},
		},
		{
			name:  "invalid path without scheme",
			input: "example.com/path",
			kinds: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []targetKind
			for _, target := range seedTargetsFromInput(tt.input) {
				got = append(got, target.Kind())
			}
			if !reflect.DeepEqual(got, tt.kinds) {
				t.Fatalf("kinds = %#v, want %#v", got, tt.kinds)
			}
		})
	}
}

func TestScanReadInputsFromListFile(t *testing.T) {
	listFile := filepath.Join(t.TempDir(), "targets.txt")
	if err := os.WriteFile(listFile, []byte(`
# cidr, ip, and url list
127.0.0.1/32
  192.0.2.10
http://127.0.0.1:8080
https://example.com

`), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := readInputs([]string{"  http://localhost:18080  ", ""}, listFile)
	if err != nil {
		t.Fatalf("readInputs() error = %v", err)
	}
	want := []string{
		"http://localhost:18080",
		"127.0.0.1/32",
		"192.0.2.10",
		"http://127.0.0.1:8080",
		"https://example.com",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inputs = %#v, want %#v", got, want)
	}
}

func TestScanBuildSeedTargetsFromBatchInputs(t *testing.T) {
	tests := []struct {
		name   string
		inputs []string
		want   map[targetKind]int
	}{
		{
			name:   "cidr",
			inputs: []string{"127.0.0.1/32"},
			want: map[targetKind]int{
				targetScan: 1,
			},
		},
		{
			name:   "iplist",
			inputs: []string{"127.0.0.1", "192.0.2.10"},
			want: map[targetKind]int{
				targetScan: 2,
			},
		},
		{
			name:   "urllist",
			inputs: []string{"http://127.0.0.1:8080", "https://example.com"},
			want: map[targetKind]int{
				targetWeb: 2,
			},
		},
		{
			name:   "mixed",
			inputs: []string{"127.0.0.1/32", "127.0.0.1", "127.0.0.1:8080", "http://example.com", "ssh://root@127.0.0.1:22", "example.com/path"},
			want: map[targetKind]int{
				targetScan:     3,
				targetWeb:      2,
				targetWeakpass: 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countEventTargetKinds(buildSeedEvents(tt.inputs, nil))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("seed target counts = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestHTTPBasicAuthCapabilityEmitsWeakpassOnlyForBasicChallenge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/basic":
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "/bearer":
			w.Header().Set("WWW-Authenticate", `Bearer realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	cmd := New(&engine.Set{})
	run := func(rawURL string, status int) []event {
		var events []event
		cmd.runHTTPBasicAuthCapability(context.Background(), flags{Timeout: 1}, newWebProbeTarget("", capSprayCheck, "", &parsers.SprayResult{
			IsValid:   true,
			UrlString: rawURL,
			Status:    status,
		}), func(event event) {
			events = append(events, event)
		})
		return events
	}

	events := run(server.URL+"/basic", http.StatusUnauthorized)
	if len(events) != 1 || !hasTargetKind(events, targetWeakpass) {
		t.Fatalf("basic auth events = %#v, want one weakpass target", events)
	}
	target, ok := events[0].Target.(weakpassTarget)
	if !ok {
		t.Fatalf("target = %T, want weakpassTarget", events[0].Target)
	}
	if target.Target.Service != "http" || target.Target.Param["path"] != "basic" {
		t.Fatalf("weakpass target = %#v, want http basic path", target.Target)
	}

	if events := run(server.URL+"/bearer", http.StatusUnauthorized); len(events) != 0 {
		t.Fatalf("bearer auth events = %#v, want none", events)
	}
	if events := run(server.URL+"/basic", http.StatusOK); len(events) != 0 {
		t.Fatalf("non-401 probe events = %#v, want none", events)
	}
}

func TestHasBasicAuthChallenge(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   bool
	}{
		{name: "basic", values: []string{`Basic realm="test"`}, want: true},
		{name: "multiple challenges", values: []string{`Digest realm="test", Basic realm="fallback"`}, want: true},
		{name: "bearer", values: []string{`Bearer realm="test"`}, want: false},
		{name: "empty", values: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasBasicAuthChallenge(tt.values); got != tt.want {
				t.Fatalf("hasBasicAuthChallenge(%#v) = %v, want %v", tt.values, got, tt.want)
			}
		})
	}
}

func TestZombieTargetFromGogoSkipsHTTPService(t *testing.T) {
	result := parsers.NewGOGOResult("127.0.0.1", "22")
	result.Protocol = "http"
	if target, ok := zombieTargetFromGogo(result); ok {
		t.Fatalf("zombieTargetFromGogo(http on ssh port) = %#v, want none", target)
	}

	var events []event
	deriveServiceResult(profile{}, capGogoPortscan, result, func(event event) {
		events = append(events, event)
	})
	if hasTargetKind(events, targetWeakpass) {
		t.Fatalf("derived HTTP service events include weakpass target: %#v", events)
	}
	if !hasTargetKind(events, targetWeb) {
		t.Fatalf("derived HTTP service events missing web target: %#v", events)
	}
}

func TestScanTargetKeys(t *testing.T) {
	tests := []struct {
		name   string
		target target
		want   string
	}{
		{
			name:   "web normalizes url and host header",
			target: newWebTarget(" raw ", "HTTP://Example.COM:80/a", "VHost.EXAMPLE"),
			want:   "http://example.com:80/a|host=vhost.example",
		},
		{
			name:   "poc normalizes fingers",
			target: newPOCTarget(" raw ", "HTTP://Example.COM", []string{"Nginx", "nginx", "PHP"}),
			want:   "http://example.com|nginx,php",
		},
		{
			name:   "weakpass includes auth",
			target: newWeakpassTarget(" raw ", mustZombieTarget(t, "ssh://root:pass@127.0.0.1:22")),
			want:   "ssh://127.0.0.1:22|root|pass",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.target.Key(); got != tt.want {
				t.Fatalf("key = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScanTargetConstructorsNormalizeFields(t *testing.T) {
	web := newWebTarget(" raw ", " http://example.com ", " Host.EXAMPLE ")
	if web.Raw != "raw" || web.URL != "http://example.com" || web.HostHeader != "host.example" {
		t.Fatalf("web target = %#v", web)
	}
	if event := targetEvent(inputSource, "", web); event.Raw != "raw" {
		t.Fatalf("target event raw = %q, want target raw", event.Raw)
	}

	poc := newPOCTarget(" raw ", " http://example.com ", []string{"Nginx", "nginx", "PHP"})
	if poc.Raw != "raw" || poc.Target != "http://example.com" || !reflect.DeepEqual(poc.Fingers, []string{"nginx", "php"}) {
		t.Fatalf("poc target = %#v", poc)
	}
}

func TestPOCCapabilitySkipsUnfingerprintedTargetsByDefault(t *testing.T) {
	cmd := New(&engine.Set{})
	var events []event
	cmd.runPOCCapability(context.Background(), flags{}, newPOCTarget("", "http://127.0.0.1", nil), func(event event) {
		events = append(events, event)
	})

	if len(events) != 0 {
		t.Fatalf("events = %#v, want none", events)
	}
}

func TestPOCCapabilitySkipsFingerWithoutMappedTemplates(t *testing.T) {
	neutronEngine := newScanTestNeutronEngine(t, scanTestTemplate("nginx-poc", "nginx"))
	index := association.NewIndex()
	index.Build(nil, neutronEngine.Get())
	cmd := New(&engine.Set{Neutron: neutronEngine, Index: index})

	var events []event
	cmd.runPOCCapability(context.Background(), flags{}, newPOCTarget("", "http://127.0.0.1", []string{"unknown"}), func(event event) {
		events = append(events, event)
	})

	if len(events) != 0 {
		t.Fatalf("events = %#v, want none", events)
	}
}

func TestSelectNeutronTemplatesRequiresFingerUnlessBroad(t *testing.T) {
	selected, filtered := engine.SelectNeutronTemplates(nil, nil, engine.NeutronExecuteOptions{})
	if len(selected) != 0 || !filtered {
		t.Fatalf("default selection = %#v filtered=%v, want empty filtered selection", selected, filtered)
	}

	selected, filtered = engine.SelectNeutronTemplates(nil, nil, engine.NeutronExecuteOptions{Broad: true})
	if len(selected) != 0 || filtered {
		t.Fatalf("broad selection = %#v filtered=%v, want unfiltered selection", selected, filtered)
	}
}

func TestScanDerivesTargetsFromResults(t *testing.T) {
	profile := profile{}
	result := parsers.NewGOGOResult("127.0.0.1", "80")
	result.Protocol = "http"
	result.Frameworks = common.Frameworks{
		"nginx": common.NewFramework("nginx", common.FrameFromFingers),
	}

	var events []event
	deriveServiceResult(profile, capGogoPortscan, result, func(event event) {
		events = append(events, event)
	})

	if !hasTargetKind(events, targetWeb) {
		t.Fatalf("derived events missing web target: %#v", events)
	}
	if !hasTargetKind(events, targetPOC) {
		t.Fatalf("derived events missing poc target: %#v", events)
	}
}

func TestScanPipelineDoesNotDispatchLootOrError(t *testing.T) {
	coll := newCollector([]string{"seed"}, nil, false, false)
	var runs int
	capabilities := []pipeline.Capability{
		wrapCapability("web", wrapRoutes(acceptsTarget(targetWeb), ""), 1, func(_ context.Context, _ event, _ func(event)) {
			runs++
		}),
	}
	p := newTestPipeline(t, context.Background(), capabilities, coll, false)
	p.Run(testSeeds(
		lootEvent("test", fingerprintLoot("http://127.0.0.1", []string{"nginx"}, false)),
		errorEventOf("test", "boom"),
	))

	if runs != 0 {
		t.Fatalf("capability runs = %d, want 0", runs)
	}
	if len(coll.seenFinger) != 1 {
		t.Fatalf("fingerprints = %d, want 1", len(coll.seenFinger))
	}
	if len(coll.errors) != 1 {
		t.Fatalf("errors = %d, want 1", len(coll.errors))
	}
}

func TestLootPriorityDefaults(t *testing.T) {
	fp := fingerprintLoot("http://127.0.0.1", []string{"nginx"}, false)
	if got := fp.Priority; got != string(priorityLow) {
		t.Fatalf("fingerprint priority = %s, want %s", got, priorityLow)
	}
	fpFocus := fingerprintLoot("http://127.0.0.1", []string{"struts2"}, true)
	if got := fpFocus.Priority; got != string(priorityHigh) {
		t.Fatalf("focus fingerprint priority = %s, want %s", got, priorityHigh)
	}
	wp := weakpassLoot(&parsers.ZombieResult{IP: "127.0.0.1", Port: "22", Service: "ssh"})
	if got := wp.Priority; got != string(priorityHigh) {
		t.Fatalf("weakpass priority = %s, want %s", got, priorityHigh)
	}
	vl := vulnLoot(&sdktypes.VulnResult{Target: "http://127.0.0.1", TemplateID: "test", Severity: "high", TemplateName: "test high"})
	if got := vl.Priority; got != string(priorityHigh) {
		t.Fatalf("vuln priority = %s, want %s", got, priorityHigh)
	}
}

func TestFocusFingerprintIsDerivedAsHighPriority(t *testing.T) {
	frame := common.NewFramework("struts2", common.FrameFromFingers)
	frame.IsFocus = true
	result := parsers.NewGOGOResult("127.0.0.1", "8080")
	result.Protocol = "http"
	result.Frameworks = common.Frameworks{"struts2": frame}

	var events []event
	deriveServiceResult(profile{}, capGogoPortscan, result, func(event event) {
		events = append(events, event)
	})

	var got *output.Loot
	for _, event := range events {
		if event.Kind == eventLoot && event.Loot != nil && event.Loot.Kind == output.LootFingerprint {
			got = event.Loot
			break
		}
	}
	if got == nil {
		t.Fatal("no fingerprint loot found")
	}
	focus, _ := got.Data["focus"].(bool)
	if !focus || got.Priority != string(priorityHigh) {
		t.Fatalf("focus fingerprint loot = %#v, want high priority focus", got)
	}
}

func TestScanPipelineFanoutAndDedup(t *testing.T) {
	coll := newCollector([]string{"seed"}, nil, false, false)
	var mu sync.Mutex
	seen := make([]string, 0)

	capabilities := []pipeline.Capability{
		wrapCapability("service-to-web", wrapRoutes(acceptsTarget(targetService), ""), 1, func(_ context.Context, e event, emit func(event)) {
			mu.Lock()
			seen = append(seen, "service-to-web")
			mu.Unlock()
			service, ok := e.Target.(serviceTarget)
			if !ok || service.Result == nil {
				return
			}
			emit(targetEvent("test", "", newWebTarget("", service.Result.GetBaseURL(), "")))
		}),
		wrapCapability("web-to-finger", wrapRoutes(acceptsTarget(targetWeb), "service-to-web"), 1, func(_ context.Context, e event, emit func(event)) {
			mu.Lock()
			seen = append(seen, "web-to-finger")
			mu.Unlock()
			web, ok := e.Target.(webTarget)
			if !ok {
				return
			}
			emit(lootEvent("test", fingerprintLoot(web.URL, []string{"nginx"}, false)))
		}),
	}

	p := newTestPipeline(t, context.Background(), capabilities, coll, false)
	result := parsers.NewGOGOResult("127.0.0.1", "80")
	result.Protocol = "http"
	service := targetEvent("test", "", newServiceTarget("", result))
	p.Run(testSeeds(service, service))

	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(seen, []string{"service-to-web", "web-to-finger"}) {
		t.Fatalf("seen capability runs = %#v", seen)
	}
	if len(coll.seenWeb) != 1 {
		t.Fatalf("web endpoints = %d, want 1", len(coll.seenWeb))
	}
	if len(coll.seenFinger) != 1 {
		t.Fatalf("fingerprints = %d, want 1", len(coll.seenFinger))
	}
	if len(coll.gogoResults) != 1 {
		t.Fatalf("gogo results = %d, want 1", len(coll.gogoResults))
	}
	if len(coll.trace) != 0 {
		t.Fatalf("trace entries = %d, want 0 without debug", len(coll.trace))
	}
}

func mustZombieTarget(t *testing.T, raw string) sdkzombie.Target {
	t.Helper()
	parsed, ok := parseInputURL(raw)
	if !ok {
		t.Fatalf("parseInputURL(%q) failed", raw)
	}
	target, ok := zombieTargetFromParsedURL(parsed, "")
	if !ok {
		t.Fatalf("zombieTargetFromParsedURL(%q) failed", raw)
	}
	return target
}

func newScanTestNeutronEngine(t *testing.T, items ...*templates.Template) *sdkneutron.Engine {
	t.Helper()
	engine, err := sdkneutron.NewEngineWithTemplates((sdkneutron.Templates{}).Merge(items))
	if err != nil {
		t.Fatalf("NewEngineWithTemplates() error = %v", err)
	}
	return engine
}

func scanTestTemplate(id string, fingers ...string) *templates.Template {
	return &templates.Template{
		Id:      id,
		Fingers: fingers,
		Info: templates.Info{
			Name:     id,
			Severity: "high",
		},
		RequestsHTTP: []*neutronhttp.Request{
			{
				Method: "GET",
				Path:   []string{"{{BaseURL}}"},
				Operators: operators.Operators{
					Matchers: []*operators.Matcher{
						{Type: "word", Words: []string{"definitely-not-present"}},
					},
				},
			},
		},
	}
}

func countEventTargetKinds(events []event) map[targetKind]int {
	counts := make(map[targetKind]int)
	for _, e := range events {
		if e.Kind == eventTarget && e.Target != nil {
			counts[e.Target.Kind()]++
		}
	}
	return counts
}

func hasTargetKind(events []event, kind targetKind) bool {
	for _, event := range events {
		if event.Kind == eventTarget && event.Target != nil && event.Target.Kind() == kind {
			return true
		}
	}
	return false
}

func TestScanPipelineDebugTrace(t *testing.T) {
	coll := newCollector([]string{"seed"}, nil, false, true)
	capabilities := []pipeline.Capability{
		wrapCapability("noop", wrapRoutes(acceptsTarget(targetWeb), ""), 1, func(context.Context, event, func(event)) {}),
	}
	p := newTestPipeline(t, context.Background(), capabilities, coll, true)
	p.Run(testSeeds(targetEvent("test", "", newWebTarget("", "http://127.0.0.1", ""))))

	if len(coll.trace) == 0 {
		t.Fatal("expected debug trace entries")
	}
	if !strings.Contains(strings.Join(coll.trace, "\n"), "dispatch") {
		t.Fatalf("trace missing dispatch entry: %#v", coll.trace)
	}
}

func TestScanPipelineCancelReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once

	coll := newCollector([]string{"seed"}, nil, false, false)
	capabilities := []pipeline.Capability{
		wrapCapability("wait", wrapRoutes(acceptsTarget(targetWeb), ""), 1, func(ctx context.Context, _ event, _ func(event)) {
			once.Do(func() { close(started) })
			<-ctx.Done()
		}),
	}
	p := newTestPipeline(t, ctx, capabilities, coll, false)

	go func() {
		p.Run(testSeeds(targetEvent("test", "", newWebTarget("", "http://127.0.0.1", ""))))
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("capability did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pipeline did not return after context cancellation")
	}
}

func TestScanSummaryJSONLines(t *testing.T) {
	coll := newCollector([]string{"seed"}, nil, false, false)
	coll.Observe(pipelineEvent{Action: pipeline.ActionAccept, Event: targetEvent("test", "", newServiceTarget("", parsers.NewGOGOResult("127.0.0.1", "80")))})
	coll.Observe(pipelineEvent{Action: pipeline.ActionAccept, Event: targetEvent("spray_check", "", newWebProbeTarget("", "spray_check", "", &parsers.SprayResult{
		IsValid:   true,
		UrlString: "http://127.0.0.1:80",
		Status:    401,
		Distance:  1,
	}))})

	out, err := coll.JSONLines()
	if err != nil {
		t.Fatalf("JSONLines() error = %v", err)
	}
	if hasANSI(out) {
		t.Fatalf("json output contains ANSI: %q", out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("json lines = %d, want 2: %q", len(lines), out)
	}
	var gogoResult parsers.GOGOResult
	if err := json.Unmarshal([]byte(lines[0]), &gogoResult); err != nil {
		t.Fatalf("unmarshal gogo json: %v", err)
	}
	if gogoResult.Ip != "127.0.0.1" || gogoResult.Port != "80" {
		t.Fatalf("gogo json = %#v", gogoResult)
	}
	var sprayResult parsers.SprayResult
	if err := json.Unmarshal([]byte(lines[1]), &sprayResult); err != nil {
		t.Fatalf("unmarshal spray json: %v", err)
	}
	if sprayResult.UrlString != "http://127.0.0.1:80" || sprayResult.Status != 401 {
		t.Fatalf("spray json = %#v", sprayResult)
	}
}

func TestScanSkipsFailedSprayProbeResults(t *testing.T) {
	cases := []struct {
		name   string
		result *parsers.SprayResult
	}{
		{
			name: "request error",
			result: &parsers.SprayResult{
				UrlString: "https://127.0.0.1:1080",
				Source:    parsers.UpgradeSource,
				Reason:    "request failed",
				ErrString: `Get "https://127.0.0.1:1080": EOF`,
			},
		},
		{
			name: "compare failed",
			result: &parsers.SprayResult{
				UrlString:  "http://127.0.0.1:32768/test.war",
				Source:     parsers.BakSource,
				Status:     401,
				BodyLength: 64,
				Title:      "json data",
				Reason:     "compare failed",
			},
		},
		{
			name: "index baseline",
			result: &parsers.SprayResult{
				IsValid:    true,
				UrlString:  "http://127.0.0.1:32768/",
				Source:     parsers.InitIndexSource,
				Status:     200,
				BodyLength: 128,
			},
		},
		{
			name: "random baseline",
			result: &parsers.SprayResult{
				IsValid:    true,
				UrlString:  "http://127.0.0.1:32768/__random__",
				Source:     parsers.InitRandomSource,
				Status:     404,
				BodyLength: 64,
			},
		},
		{
			name: "fuzzy baseline",
			result: &parsers.SprayResult{
				IsValid:    true,
				IsFuzzy:    true,
				UrlString:  "http://127.0.0.1:32768/orders.log.old",
				Source:     parsers.AppendSource,
				Status:     401,
				BodyLength: 64,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			coll := newCollector([]string{"seed"}, &buf, false, false)
			coll.Observe(pipelineEvent{
				Action: pipeline.ActionAccept,
				Event:  targetEvent("spray_check", "", newWebProbeTarget("", "spray_check", "", tc.result)),
			})

			if got := buf.String(); got != "" {
				t.Fatalf("stream output = %q, want empty", got)
			}
			if len(coll.sprayResults) != 0 {
				t.Fatalf("spray results = %d, want 0", len(coll.sprayResults))
			}
			var derived []event
			deriveWebProbeResult(profile{}, "spray_check", tc.result, "", func(event event) {
				derived = append(derived, event)
			})
			if len(derived) != 0 {
				t.Fatalf("derived events = %#v, want none", derived)
			}
		})
	}
}

func TestScanSkipsInternalPluginCheckBaseline(t *testing.T) {
	result := &parsers.SprayResult{
		IsValid:    true,
		UrlString:  "http://127.0.0.1:8081",
		Source:     parsers.CheckSource,
		Status:     500,
		BodyLength: 114,
		Title:      "json data",
	}
	event := targetEvent(capSprayPlugins, "", newWebProbeTarget("", capSprayPlugins, "", result))
	if line := formatEventLine(event, false); line != "" {
		t.Fatalf("plugin check baseline line = %q, want empty", line)
	}

	var buf bytes.Buffer
	coll := newCollector([]string{"seed"}, &buf, false, false)
	coll.Observe(pipelineEvent{Action: pipeline.ActionAccept, Event: event})
	if got := buf.String(); got != "" {
		t.Fatalf("stream output = %q, want empty", got)
	}
	if len(coll.sprayResults) != 0 {
		t.Fatalf("spray results = %d, want 0", len(coll.sprayResults))
	}

	checkEvent := targetEvent(capSprayCheck, "", newWebProbeTarget("", capSprayCheck, "", result))
	if line := formatEventLine(checkEvent, false); !strings.Contains(line, "[web] http://127.0.0.1:8081 500 114") {
		t.Fatalf("primary spray_check line = %q, want user-facing web prefix", line)
	}
}

func TestScanStreamsAcceptedResults(t *testing.T) {
	var buf bytes.Buffer
	coll := newCollector([]string{"seed"}, &buf, true, false)
	result := parsers.NewGOGOResult("127.0.0.1", "80")
	result.Protocol = "http"
	result.Status = "200"

	coll.Observe(pipelineEvent{Action: pipeline.ActionAccept, Event: targetEvent(capGogoPortscan, "", newServiceTarget("", result))})

	raw := buf.String()
	if !hasANSI(raw) {
		t.Fatalf("colored stream output missing ANSI: %q", raw)
	}
	out := output.StripANSI(raw)
	if !strings.Contains(out, "[web] http://127.0.0.1:80 200 http") {
		t.Fatalf("stream output = %q", out)
	}
	if strings.Contains(out, "##") {
		t.Fatalf("stream output should be single-line event output: %q", out)
	}
}

func TestScanColorizesWebProbePrefixOnly(t *testing.T) {
	var buf bytes.Buffer
	coll := newCollector([]string{"seed"}, &buf, true, false)
	coll.Observe(pipelineEvent{Action: pipeline.ActionAccept, Event: targetEvent(capSprayPlugins, "", newWebProbeTarget("", capSprayPlugins, "", &parsers.SprayResult{
		IsValid:    true,
		UrlString:  "http://127.0.0.1:32768/test.war",
		Source:     parsers.BakSource,
		Status:     401,
		BodyLength: 64,
		Spended:    26,
		Title:      "json data",
	}))})

	raw := buf.String()
	for _, want := range []string{
		logs.Green("[web]"),
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("colored output missing %q in %q", want, raw)
		}
	}
	if strings.Contains(raw, logs.Yellow("401")) || strings.Contains(raw, logs.Green(`"json data"`)) {
		t.Fatalf("scan output should not parse and color parser fields: %q", raw)
	}
	out := output.StripANSI(raw)
	if !strings.Contains(out, `[web] http://127.0.0.1:32768/test.war 401 64 26ms "json data"`) {
		t.Fatalf("plain colored output shape changed: %q", out)
	}
}

func TestScanUnifiesFrameworkOutput(t *testing.T) {
	frameworks := common.Frameworks{
		"nginx":   common.NewFramework("nginx", common.FrameFromFingers),
		"struts2": common.NewFramework("struts2", common.FrameFromFingers),
	}
	gogoResult := parsers.NewGOGOResult("127.0.0.1", "8080")
	gogoResult.Protocol = "http"
	gogoResult.Status = "200"
	gogoResult.Frameworks = frameworks

	sprayResult := &parsers.SprayResult{
		IsValid:    true,
		UrlString:  "http://127.0.0.1:8080",
		Source:     parsers.CheckSource,
		Status:     200,
		BodyLength: 12,
		Frameworks: frameworks,
	}

	lines := []string{
		formatEventLine(targetEvent(capGogoPortscan, "", newServiceTarget("", gogoResult)), false),
		formatEventLine(targetEvent(capSprayCheck, "", newWebProbeTarget("", capSprayCheck, "", sprayResult)), false),
	}
	for _, line := range lines {
		if !strings.Contains(line, "[nginx,struts2]") {
			t.Fatalf("framework output is not unified: %q", line)
		}
		for _, polluted := range []string{"fp=", "frameworks=", "||", "[nginx] [struts2]"} {
			if strings.Contains(line, polluted) {
				t.Fatalf("framework output contains old style %q: %q", polluted, line)
			}
		}
	}
}

func TestScanLootPriorityUsesFocusOutputOnly(t *testing.T) {
	plain := formatEventLine(lootEvent(capSprayCheck, fingerprintLoot("http://127.0.0.1", []string{"nginx"}, false)), false)
	if plain != "" {
		t.Fatalf("plain non-focus fingerprint output = %q, want empty", plain)
	}
	plainFocus := formatEventLine(lootEvent(capSprayCheck, fingerprintLoot("http://127.0.0.1", []string{"struts2"}, true)), false)
	if strings.Contains(plain, " low ") || strings.Contains(plain, " high ") {
		t.Fatalf("plain loot output should not print priority text: %q", plain)
	}
	if !strings.Contains(plainFocus, "[fingerprint]") || !strings.Contains(plainFocus, "struts2") {
		t.Fatalf("plain focus output shape changed: %q", plainFocus)
	}

	colored := formatEventLine(lootEvent(capSprayCheck, fingerprintLoot("http://127.0.0.1", []string{"struts2"}, true)), true)
	if strings.Contains(output.StripANSI(colored), " high ") {
		t.Fatalf("colored loot output should not print priority text: %q", colored)
	}
	if !strings.Contains(colored, logs.Red("[fingerprint]")) {
		t.Fatalf("colored loot output should encode high priority in color: %q", colored)
	}
}

func TestScanStreamsWithoutColor(t *testing.T) {
	var buf bytes.Buffer
	coll := newCollector([]string{"seed"}, &buf, false, false)
	result := parsers.NewGOGOResult("127.0.0.1", "80")
	result.Protocol = "http"
	result.Status = "200"

	coll.Observe(pipelineEvent{Action: pipeline.ActionAccept, Event: targetEvent(capGogoPortscan, "", newServiceTarget("", result))})

	out := buf.String()
	if hasANSI(out) {
		t.Fatalf("uncolored stream output contains ANSI: %q", out)
	}
	if !strings.Contains(out, "[web] http://127.0.0.1:80 200 http") {
		t.Fatalf("stream output = %q", out)
	}
}

func TestScanSummaryUsesStructuredFields(t *testing.T) {
	coll := newCollector([]string{"seed"}, nil, false, false)
	result := parsers.NewGOGOResult("127.0.0.1", "80")
	result.Protocol = "http"
	result.Status = "200"

	coll.Observe(pipelineEvent{Action: pipeline.ActionCapabilityStart, Capability: capGogoPortscan, Event: targetEvent("", "", newScanTarget("", "127.0.0.1", ""))})
	coll.Observe(pipelineEvent{Action: pipeline.ActionAccept, Event: targetEvent(capGogoPortscan, "", newServiceTarget("", result))})
	coll.Finish()

	out := coll.String()
	for _, want := range []string{
		"[summary] completed 1 target 1 service 0 web 0 probes 0 fingerprints 0 loots 0 errors 0 tasks 0 requests",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary output missing %q:\n%s", want, out)
		}
	}
}

func TestScanSummaryAggregatesEngineStats(t *testing.T) {
	coll := newCollector([]string{"seed"}, nil, false, false)
	coll.Observe(pipelineEvent{Action: pipeline.ActionAccept, Event: statsEvent(capGogoPortscan, sdktypes.Stats{
		Engine:   "gogo",
		Task:     "scan",
		Targets:  2,
		Tasks:    4,
		Requests: 4,
		Results:  1,
		Duration: 10 * time.Millisecond,
	})})
	coll.Observe(pipelineEvent{Action: pipeline.ActionAccept, Event: statsEvent(capSprayCheck, sdktypes.Stats{
		Engine:   "spray",
		Task:     "check",
		Targets:  1,
		Tasks:    3,
		Requests: 5,
		Results:  2,
		Errors:   1,
		Duration: 20 * time.Millisecond,
	})})
	coll.Finish()

	out := coll.String()
	if !strings.Contains(out, "7 tasks 9 requests") {
		t.Fatalf("summary missing aggregated stats:\n%s", out)
	}

	report := coll.ReportMarkdown()
	for _, want := range []string{"| Tasks | 7 |", "| Requests | 9 |"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestProjectorSlowStreamDoesNotHoldStateLock(t *testing.T) {
	writer := &blockingWriter{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	coll := newCollector([]string{"seed"}, writer, false, false)
	result := parsers.NewGOGOResult("127.0.0.1", "80")
	result.Protocol = "http"
	result.Status = "200"

	observeDone := make(chan struct{})
	go func() {
		coll.Observe(pipelineEvent{Action: pipeline.ActionAccept, Event: targetEvent(capGogoPortscan, "", newServiceTarget("", result))})
		close(observeDone)
	}()

	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("stream writer was not called")
	}

	jsonDone := make(chan struct{})
	go func() {
		if _, err := coll.JSONLines(); err != nil {
			t.Errorf("JSONLines() error = %v", err)
		}
		close(jsonDone)
	}()

	select {
	case <-jsonDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("projector state lock was held while writing stream output")
	}

	close(writer.release)
	select {
	case <-observeDone:
	case <-time.After(time.Second):
		t.Fatal("Observe did not finish after stream writer was released")
	}
}

func TestScanPlainTextStripsANSI(t *testing.T) {
	coll := newCollector([]string{"seed"}, nil, false, false)
	coll.Observe(pipelineEvent{Action: pipeline.ActionAccept, Event: targetEvent("spray_check", "", newWebProbeTarget("", "spray_check", "", &parsers.SprayResult{
		IsValid:    true,
		UrlString:  "http://127.0.0.1:80",
		Source:     parsers.CheckSource,
		Status:     200,
		BodyLength: 12,
		Distance:   1,
	}))})
	coll.Finish()

	out := coll.PlainText()
	if hasANSI(out) {
		t.Fatalf("plain text output contains ANSI: %q", out)
	}
	if !strings.Contains(out, "[web] http://127.0.0.1:80 200 12 sim:1") {
		t.Fatalf("plain text output missing parser content: %q", out)
	}
}

func TestScanAggregatesAssets(t *testing.T) {
	coll := newCollector([]string{"seed"}, nil, false, false)
	service := parsers.NewGOGOResult("127.0.0.1", "8080")
	service.Protocol = "http"
	service.Midware = "http"

	coll.Observe(pipelineEvent{Action: pipeline.ActionAccept, Event: targetEvent(capGogoPortscan, "", newServiceTarget("", service))})
	coll.Observe(pipelineEvent{Action: pipeline.ActionAccept, Event: targetEvent(capSprayCheck, "", newWebProbeTarget("", capSprayCheck, "", &parsers.SprayResult{
		IsValid:    true,
		UrlString:  "http://127.0.0.1:8080/admin",
		Status:     200,
		Title:      "admin",
		Distance:   1,
		Frameworks: common.Frameworks{"nginx": {Name: "nginx", IsFocus: true}},
	}))})
	coll.Finish()

	result := coll.StructuredResult()
	if len(result.Assets) != 1 {
		t.Fatalf("assets = %d, want 1: %#v", len(result.Assets), result.Assets)
	}
	kinds := assetItemKindCounts(result.Assets[0].Items)
	for _, kind := range []string{output.AssetItemService, output.AssetItemPath, output.AssetItemFingerprint} {
		if kinds[kind] != 1 {
			t.Fatalf("asset item %s count = %d, want 1 in %#v", kind, kinds[kind], result.Assets[0].Items)
		}
	}
}

func assetItemKindCounts(items []output.AssetItem) map[string]int {
	counts := make(map[string]int)
	for _, item := range items {
		counts[item.Kind]++
	}
	return counts
}

func TestScanOutputFileWritesPlainTextWithoutChangingStdout(t *testing.T) {
	sprayEng, _ := spray.NewEngine(nil)
	cmd := New(&engine.Set{Spray: sprayEng})
	file := filepath.Join(t.TempDir(), "scan.txt")
	var stream bytes.Buffer

	out, _, err := cmd.ExecuteStructured(context.Background(), []string{"-i", "http://127.0.0.1:1", "--mode", "quick", "--timeout", "1", "-f", file}, &stream)
	if err != nil {
		t.Fatalf("ExecuteStructured() error = %v", err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	fileOut := string(data)
	if hasANSI(fileOut) {
		t.Fatalf("file output contains ANSI: %q", fileOut)
	}
	if !strings.Contains(fileOut, "[summary] completed") {
		t.Fatalf("file output missing summary: %q", fileOut)
	}
	if !strings.Contains(output.StripANSI(out), "[summary] completed") {
		t.Fatalf("stdout output missing summary: %q", out)
	}
	if strings.Contains(out, "[scan.web] ") {
		t.Fatalf("stdout output should not repeat streamed events: %q", out)
	}
	if !strings.Contains(output.StripANSI(stream.String()), "http://127.0.0.1:1") {
		t.Fatalf("stream output missing event line: %q", stream.String())
	}
	if strings.Contains(output.StripANSI(stream.String()), "type=web") {
		t.Fatalf("stream output contains key/value pollution: %q", stream.String())
	}
}

func hasANSI(value string) bool {
	return strings.Contains(value, "\x1b[")
}

type blockingWriter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return len(p), nil
}

func TestScanReportMarkdown(t *testing.T) {
	coll := newCollector([]string{"seed"}, nil, false, false)
	coll.Observe(pipelineEvent{Action: pipeline.ActionCapabilityStart, Capability: capGogoPortscan, Event: targetEvent("", "", newScanTarget("", "127.0.0.1", ""))})
	coll.Observe(pipelineEvent{Action: pipeline.ActionAccept, Event: targetEvent(capGogoPortscan, "", newServiceTarget("", parsers.NewGOGOResult("127.0.0.1", "80")))})
	coll.Observe(pipelineEvent{Action: pipeline.ActionAccept, Event: targetEvent("spray_check", "", newWebProbeTarget("", "spray_check", "", &parsers.SprayResult{
		IsValid:   true,
		UrlString: "http://127.0.0.1:80",
		Status:    200,
		Distance:  1,
	}))})
	coll.Finish()

	report := coll.ReportMarkdown()
	if hasANSI(report) {
		t.Fatalf("report contains ANSI: %q", report)
	}
	for _, want := range []string{"# Scan Report", "## Metrics", "## Open Services"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestPipelinePerRouteDedupIsolation(t *testing.T) {
	// Two capabilities both subscribe to seed webTargets.
	// The same URL sent as seed should be deduped within each route,
	// but both capabilities should still process it once.
	var mu sync.Mutex
	runs := make(map[string]int)

	capabilities := []pipeline.Capability{
		wrapCapability("cap-a", wrapRoutes(acceptsTarget(targetWeb), ""), 1, func(_ context.Context, _ event, _ func(event)) {
			mu.Lock()
			runs["cap-a"]++
			mu.Unlock()
		}),
		wrapCapability("cap-b", wrapRoutes(acceptsTarget(targetWeb), ""), 1, func(_ context.Context, _ event, _ func(event)) {
			mu.Lock()
			runs["cap-b"]++
			mu.Unlock()
		}),
	}

	p := newTestPipeline(t, context.Background(), capabilities, nil, false)
	url1 := targetEvent("test", "", newWebTarget("", "http://127.0.0.1", ""))
	url2 := targetEvent("test", "", newWebTarget("", "http://127.0.0.2", ""))
	// Send url1 twice + url2 once
	p.Run(testSeeds(url1, url1, url2))

	mu.Lock()
	defer mu.Unlock()
	// Each cap runs once per unique URL = 2 times each
	if runs["cap-a"] != 2 {
		t.Fatalf("cap-a runs = %d, want 2", runs["cap-a"])
	}
	if runs["cap-b"] != 2 {
		t.Fatalf("cap-b runs = %d, want 2", runs["cap-b"])
	}
}

func TestPipelineCleanupFreesAllDedupMaps(t *testing.T) {
	capabilities := []pipeline.Capability{
		wrapCapability("producer", wrapRoutes(acceptsTarget(targetScan), ""), 1, func(_ context.Context, _ event, emit func(event)) {
			emit(targetEvent("producer", "", newWebTarget("", "http://10.0.0.1", "")))
		}),
		wrapCapability("consumer", wrapRoutes(acceptsTarget(targetWeb), "producer"), 1, func(_ context.Context, _ event, _ func(event)) {}),
	}

	p := newTestPipeline(t, context.Background(), capabilities, nil, false)

	// Before Run: dedup maps exist and are empty
	before := p.RouteStats()
	for key, size := range before {
		if size != 0 {
			t.Fatalf("before Run: route %q has %d entries, want 0", key, size)
		}
	}
	if len(before) != 2 {
		t.Fatalf("before Run: %d routes, want 2", len(before))
	}

	p.Run(testSeeds(targetEvent("test", "", newScanTarget("", "10.0.0.0/32", ""))))

	// After Run: all dedup maps freed (nil = -1)
	after := p.RouteStats()
	for key, size := range after {
		if size != -1 {
			t.Fatalf("after Run: route %q size = %d, want -1 (freed)", key, size)
		}
	}
}

func TestPipelineDAGValidationRejectsCycle(t *testing.T) {
	capabilities := []pipeline.Capability{
		{
			Name:   "A",
			Routes: []pipeline.Route{{From: "B", Accept: func(pipeline.Event) bool { return true }}},
			Worker: 1,
			Run:    func(context.Context, pipeline.Event, func(pipeline.Event)) {},
		},
		{
			Name:   "B",
			Routes: []pipeline.Route{{From: "A", Accept: func(pipeline.Event) bool { return true }}},
			Worker: 1,
			Run:    func(context.Context, pipeline.Event, func(pipeline.Event)) {},
		},
	}

	_, err := pipeline.New(context.Background(), pipeline.Config{Capabilities: capabilities})
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error = %q, want cycle mention", err)
	}
}

func TestPipelineDAGValidationAcceptsValidGraph(t *testing.T) {
	capabilities := []pipeline.Capability{
		{
			Name:   "A",
			Routes: []pipeline.Route{{From: "", Accept: func(pipeline.Event) bool { return true }}},
			Worker: 1,
			Run:    func(context.Context, pipeline.Event, func(pipeline.Event)) {},
		},
		{
			Name:   "B",
			Routes: []pipeline.Route{{From: "A", Accept: func(pipeline.Event) bool { return true }}},
			Worker: 1,
			Run:    func(context.Context, pipeline.Event, func(pipeline.Event)) {},
		},
		{
			Name:   "C",
			Routes: []pipeline.Route{{From: "A"}, {From: "B"}},
			Worker: 1,
			Run:    func(context.Context, pipeline.Event, func(pipeline.Event)) {},
		},
	}

	_, err := pipeline.New(context.Background(), pipeline.Config{Capabilities: capabilities})
	if err != nil {
		t.Fatalf("unexpected error for valid DAG: %v", err)
	}
}

func TestPipelineRouteDedupPreventsRedundantWork(t *testing.T) {
	// producer emits the same webTarget 3 times; consumer should only run once.
	var consumerRuns int
	capabilities := []pipeline.Capability{
		wrapCapability("producer", wrapRoutes(acceptsTarget(targetScan), ""), 1, func(_ context.Context, _ event, emit func(event)) {
			for i := 0; i < 3; i++ {
				emit(targetEvent("producer", "", newWebTarget("", "http://dup.example.com", "")))
			}
		}),
		wrapCapability("consumer", wrapRoutes(acceptsTarget(targetWeb), "producer"), 1, func(_ context.Context, _ event, _ func(event)) {
			consumerRuns++
		}),
	}

	p := newTestPipeline(t, context.Background(), capabilities, nil, false)
	p.Run(testSeeds(targetEvent("test", "", newScanTarget("", "10.0.0.1", ""))))

	if consumerRuns != 1 {
		t.Fatalf("consumer runs = %d, want 1 (dedup should suppress duplicates)", consumerRuns)
	}
}
