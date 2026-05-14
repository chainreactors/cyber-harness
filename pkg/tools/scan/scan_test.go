package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/tools/engines"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/neutron/operators"
	neutronhttp "github.com/chainreactors/neutron/protocols/http"
	"github.com/chainreactors/neutron/templates"
	"github.com/chainreactors/parsers"
	sdkgogo "github.com/chainreactors/sdk/gogo"
	sdkneutron "github.com/chainreactors/sdk/neutron"
	"github.com/chainreactors/sdk/pkg/association"
	"github.com/chainreactors/sdk/spray"
	sdkzombie "github.com/chainreactors/sdk/zombie"
)

func TestScanRunsWithOnlySprayStage(t *testing.T) {
	cmd := New(&engines.Set{Spray: spray.NewEngine(nil)})
	out, err := cmd.Execute(context.Background(), []string{"-i", "http://127.0.0.1:1", "--mode", "quick", "--timeout", "1"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out, "[scan] completed") {
		t.Fatalf("output missing summary: %q", out)
	}
}

func TestScanProfilesAssembleCapabilities(t *testing.T) {
	quick, err := profileForMode("quick")
	if err != nil {
		t.Fatalf("quick profile error = %v", err)
	}
	for _, name := range []string{capGogoPortscan, capSprayCheck, capSprayFinger, capCoreWeb, capSprayCommon, capSprayBackup, capSprayActive, capSprayCrawl, capZombieWeakpass, capNeutronPOC} {
		if !quick.Enabled(name) {
			t.Fatalf("quick profile missing %s", name)
		}
	}
	for _, name := range []string{capSprayBrute} {
		if quick.Enabled(name) {
			t.Fatalf("quick profile should not enable %s", name)
		}
	}

	full, err := profileForMode("full")
	if err != nil {
		t.Fatalf("full profile error = %v", err)
	}
	for _, name := range []string{capGogoPortscan, capSprayCheck, capSprayFinger, capCoreWeb, capSprayCommon, capSprayBackup, capSprayActive, capSprayCrawl, capSprayBrute, capZombieWeakpass, capNeutronPOC} {
		if !full.Enabled(name) {
			t.Fatalf("full profile missing %s", name)
		}
	}
	if full.AllowBroadPOC {
		t.Fatal("full profile should not run broad POC checks without --broad-poc")
	}
}

func TestScanAcceptsBroadPOCFlag(t *testing.T) {
	cmd := New(&engines.Set{Spray: spray.NewEngine(nil)})
	out, err := cmd.Execute(context.Background(), []string{"-i", "http://127.0.0.1:1", "--mode", "full", "--broad-poc", "--timeout", "1"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out, "[scan] completed") {
		t.Fatalf("output missing summary: %q", out)
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
	if opts.Discovery.Ports != scanQuickDefaultPorts || opts.Discovery.Version != scanGogoVersionLevel || opts.hasDiscoveryOverrides() {
		t.Fatalf("quick discovery defaults = %#v", opts.Discovery)
	}

	opts = resolveScanOptions(flags{Mode: scanModeFull})
	if opts.Discovery.Ports != scanFullDefaultPorts || opts.Discovery.Version != scanGogoVersionLevel || opts.hasDiscoveryOverrides() {
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
	cmd := New(&engines.Set{}, WithLogger(telemetry.NewLogger(telemetry.LogConfig{Output: &logBuf})))
	profile := profile{Capabilities: capabilitySet(capGogoPortscan)}
	caps := cmd.buildCapabilities(flags{}, scanOptions{Discovery: discoveryOptions{Ports: "top100", Explicit: true}}, profile)
	if len(caps) != 0 {
		t.Fatalf("capabilities = %d, want 0 without gogo engine", len(caps))
	}
	if !strings.Contains(logBuf.String(), "port ignored unavailable") {
		t.Fatalf("warning log missing discovery ignore message: %q", logBuf.String())
	}
}

func TestScanWarnsWhenCredentialFlagsCannotAffectWeakpassCapability(t *testing.T) {
	var logBuf bytes.Buffer
	cmd := New(&engines.Set{}, WithLogger(telemetry.NewLogger(telemetry.LogConfig{Output: &logBuf})))
	profile := profile{Capabilities: capabilitySet(capZombieWeakpass)}
	caps := cmd.buildCapabilities(flags{}, scanOptions{Credentials: credentialOptions{Users: []string{"root"}}}, profile)
	if len(caps) != 0 {
		t.Fatalf("capabilities = %d, want 0 without zombie engine", len(caps))
	}
	if !strings.Contains(logBuf.String(), "user,pwd ignored unavailable") {
		t.Fatalf("warning log missing credential ignore message: %q", logBuf.String())
	}
}

func TestScanWarnsWhenWebFlagsCannotAffectSprayCapability(t *testing.T) {
	var logBuf bytes.Buffer
	cmd := New(&engines.Set{}, WithLogger(telemetry.NewLogger(telemetry.LogConfig{Output: &logBuf})))
	profile := profile{Capabilities: capabilitySet(capSprayCommon)}
	caps := cmd.buildCapabilities(flags{}, scanOptions{Web: webOptions{Dictionaries: []string{"paths.txt"}}}, profile)
	if len(caps) != 0 {
		t.Fatalf("capabilities = %d, want 0 without spray engine", len(caps))
	}
	if !strings.Contains(logBuf.String(), "dict,rule,word,default-dict,advance ignored unavailable") {
		t.Fatalf("warning log missing web ignore message: %q", logBuf.String())
	}
}

func TestSprayCapabilityAppliesWebStrategyOptions(t *testing.T) {
	var got sprayCheckOptions
	web := webOptions{
		Dictionaries: []string{"paths.txt"},
		Rules:        []string{"rules.txt"},
		Word:         "admin{?ld#2}",
		DefaultDict:  true,
		Advance:      true,
	}
	cmd := &Command{engines: &engines.Set{Capacity: distributeCapacity(1000)}}
	cap := sprayCapability(cmd, flags{SprayThreads: 7, Timeout: 9}, web, capSprayCommon, sprayCheckOptions{CommonPlugin: true}, func(_ context.Context, f flags, gotWeb webOptions, input target, source string, opts sprayCheckOptions, emit emitFunc) {
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
	cap.Run(context.Background(), targetEvent("test", "raw", newWebTarget("raw", "http://127.0.0.1", "")), func(event event) {
		emitted = append(emitted, event)
	})

	if !reflect.DeepEqual(got.Dictionaries, web.Dictionaries) || !reflect.DeepEqual(got.Rules, web.Rules) {
		t.Fatalf("spray dictionaries/rules = %#v/%#v", got.Dictionaries, got.Rules)
	}
	if got.Word != web.Word || !got.DefaultDict || !got.Advance {
		t.Fatalf("spray web strategy options = %#v", got)
	}
	if got.Threads != 7 || got.Timeout != 9 || !got.CommonPlugin {
		t.Fatalf("spray base options = %#v", got)
	}
	if len(emitted) != 1 || emitted[0].Target == nil {
		t.Fatalf("emitted = %#v, want one target event", emitted)
	}
}

func TestApplyWebStrategyOptionsEnablesReconAndPreservesCapabilityDefaults(t *testing.T) {
	web := webOptions{
		Dictionaries: []string{"paths.txt"},
		Rules:        []string{"rules.txt"},
		Word:         "admin",
	}
	opts := applyWebStrategyOptions(flags{SprayThreads: 7, Timeout: 9}, web, sprayCheckOptions{DefaultDict: true, BakPlugin: true})
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

func TestScanBuildCapabilitiesUsesCapacityDrivenWorkers(t *testing.T) {
	cmd := New(&engines.Set{
		Gogo:  sdkgogo.NewEngine(nil),
		Spray: spray.NewEngine(nil),
	})
	profile := profile{Capabilities: capabilitySet(
		capGogoPortscan,
		capSprayCheck,
		capSprayFinger,
		capSprayCommon,
		capSprayBackup,
		capSprayActive,
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
		capSprayFinger:  5,
		capSprayCommon:  5,
		capSprayBackup:  5,
		capSprayActive:  5,
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
	cmd := New(&engines.Set{
		Gogo:  sdkgogo.NewEngine(nil),
		Spray: spray.NewEngine(nil),
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
	cmd := New(&engines.Set{
		Gogo:  sdkgogo.NewEngine(nil),
		Spray: spray.NewEngine(nil),
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
			kinds: []targetKind{targetWeb, targetWeakpass},
		},
		{
			name:  "hostport web",
			input: "127.0.0.1:8080",
			kinds: []targetKind{targetScan, targetWeb, targetWeakpass},
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
				targetWeb:      2,
				targetWeakpass: 2,
			},
		},
		{
			name:   "mixed",
			inputs: []string{"127.0.0.1/32", "127.0.0.1", "127.0.0.1:8080", "http://example.com", "ssh://root@127.0.0.1:22", "example.com/path"},
			want: map[targetKind]int{
				targetScan:     3,
				targetWeb:      2,
				targetWeakpass: 3,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countTargetKinds(buildSeedTargets(tt.inputs, nil))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("seed target counts = %#v, want %#v", got, tt.want)
			}
		})
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
	cmd := New(&engines.Set{})
	var events []event
	cmd.runPOCCapability(context.Background(), flags{}, newPOCTarget("", "http://127.0.0.1", nil), func(event event) {
		events = append(events, event)
	})

	if len(events) != 0 {
		t.Fatalf("events = %#v, want none", events)
	}
}

func TestPOCCapabilitySkipsFingerWithoutMappedTemplates(t *testing.T) {
	engine := newScanTestNeutronEngine(t, scanTestTemplate("nginx-poc", "nginx"))
	index := association.NewFingerPOCIndex()
	index.BuildFromTemplates(engine.Get())
	cmd := New(&engines.Set{Neutron: engine, Index: index})

	var events []event
	cmd.runPOCCapability(context.Background(), flags{}, newPOCTarget("", "http://127.0.0.1", []string{"unknown"}), func(event event) {
		events = append(events, event)
	})

	if len(events) != 0 {
		t.Fatalf("events = %#v, want none", events)
	}
}

func TestSelectNeutronTemplatesRequiresFingerUnlessBroad(t *testing.T) {
	selected, filtered := selectNeutronTemplates(nil, nil, neutronExecuteOptions{})
	if len(selected) != 0 || !filtered {
		t.Fatalf("default selection = %#v filtered=%v, want empty filtered selection", selected, filtered)
	}

	selected, filtered = selectNeutronTemplates(nil, nil, neutronExecuteOptions{Broad: true})
	if len(selected) != 0 || filtered {
		t.Fatalf("broad selection = %#v filtered=%v, want unfiltered selection", selected, filtered)
	}
}

func TestScanDerivesTargetsFromResults(t *testing.T) {
	profile := profile{AllowBroadPOC: true}
	result := parsers.NewGOGOResult("127.0.0.1", "80")
	result.Protocol = "http"

	var events []event
	deriveServiceResult(profile, capGogoPortscan, serviceResult{Result: result}, func(event event) {
		events = append(events, event)
	})

	if !hasTargetKind(events, targetWeb) {
		t.Fatalf("derived events missing web target: %#v", events)
	}
	if !hasTargetKind(events, targetPOC) {
		t.Fatalf("derived events missing poc target: %#v", events)
	}
}

func TestScanPipelineDoesNotDispatchFindingOrError(t *testing.T) {
	projector := newProjector([]string{"seed"}, projectorOptions{})
	var runs int
	capabilities := []capability{
		{
			Name:   "web",
			Accept: acceptsTarget(targetWeb),
			Worker: 1,
			Run: func(context.Context, event, emitFunc) {
				runs++
			},
		},
	}
	pipeline := newPipeline(context.Background(), capabilities, projector, false)
	pipeline.Run([]event{
		findingEvent("test", fingerprintFinding{Target: "http://127.0.0.1", Fingers: []string{"nginx"}}),
		errorEventOf("test", "boom"),
	})

	if runs != 0 {
		t.Fatalf("capability runs = %d, want 0", runs)
	}
	if len(projector.data.fingerprints) != 1 {
		t.Fatalf("fingerprints = %d, want 1", len(projector.data.fingerprints))
	}
	if len(projector.data.errors) != 1 {
		t.Fatalf("errors = %d, want 1", len(projector.data.errors))
	}
}

func TestFindingPriorityDefaults(t *testing.T) {
	if got := (fingerprintFinding{Target: "http://127.0.0.1", Fingers: []string{"nginx"}}).Priority(); got != priorityLow {
		t.Fatalf("fingerprint priority = %s, want %s", got, priorityLow)
	}
	if got := (weakpassFinding{Result: &parsers.ZombieResult{IP: "127.0.0.1", Port: "22", Service: "ssh"}}).Priority(); got != priorityHigh {
		t.Fatalf("weakpass priority = %s, want %s", got, priorityHigh)
	}
	if got := (vulnFinding{Message: "[vuln] http://127.0.0.1 test high"}).Priority(); got != priorityHigh {
		t.Fatalf("vuln priority = %s, want %s", got, priorityHigh)
	}
}

func TestScanPipelineDispatchesHighPriorityFindingToAgentVerifier(t *testing.T) {
	projector := newProjector([]string{"seed"}, projectorOptions{})
	var runs int
	capabilities := []capability{
		{
			Name:   capAgentVerify,
			Worker: 1,
			Accept: func(e event) bool {
				return e.Kind == eventFinding && e.Finding != nil && e.Finding.Kind() != findingVerification && e.Finding.Priority().atLeast(priorityHigh)
			},
			Run: func(_ context.Context, e event, emit emitFunc) {
				runs++
				emit(findingEvent(capAgentVerify, verificationFinding{
					OriginalKey:      e.Finding.Key(),
					OriginalKind:     e.Finding.Kind(),
					OriginalPriority: e.Finding.Priority(),
					Status:           verificationConfirmed,
					Target:           findingTarget(e.Finding),
					Summary:          "confirmed by test",
				}))
			},
		},
	}
	pipeline := newPipeline(context.Background(), capabilities, projector, false)
	pipeline.Run([]event{
		findingEvent("test", fingerprintFinding{Target: "http://127.0.0.1", Fingers: []string{"nginx"}}),
		findingEvent("test", vulnFinding{Message: "[vuln] http://127.0.0.1 test high"}),
	})

	if runs != 1 {
		t.Fatalf("verifier runs = %d, want 1", runs)
	}
	if len(projector.data.verifications) != 1 {
		t.Fatalf("verifications = %d, want 1", len(projector.data.verifications))
	}
	if projector.data.verifications[0].Finding.Status != verificationConfirmed {
		t.Fatalf("verification status = %s, want %s", projector.data.verifications[0].Finding.Status, verificationConfirmed)
	}
}

func TestAgentVerifyCapabilityUsesProviderAndEmitsVerification(t *testing.T) {
	var calls int
	verifyFn := func(_ context.Context, prompt, systemPrompt, model string, maxTokens int) (string, error) {
		calls++
		if model != "test-model" {
			t.Fatalf("model = %q, want test-model", model)
		}
		return "status: confirmed\nsummary: direct evidence supports the vulnerability\nevidence: template matched", nil
	}
	cmd := New(&engines.Set{}, WithVerifyFunc(verifyFn), WithVerificationConfig(VerificationConfig{Model: "test-model"}))
	flags := flags{Verify: "high", VerifyTimeout: 5}
	cap, ok := cmd.agentVerifyCapability(flags)
	if !ok {
		t.Fatal("agent verifier capability was not built")
	}
	projector := newProjector([]string{"seed"}, projectorOptions{})
	pipeline := newPipeline(context.Background(), []capability{cap}, projector, false)
	pipeline.Run([]event{
		findingEvent(capNeutronPOC, vulnFinding{Message: "[vuln] http://127.0.0.1 test high"}),
	})

	if len(projector.data.verifications) != 1 {
		t.Fatalf("verifications = %d, want 1", len(projector.data.verifications))
	}
	got := projector.data.verifications[0].Finding
	if got.Status != verificationConfirmed {
		t.Fatalf("status = %s, want %s", got.Status, verificationConfirmed)
	}
	if got.Target != "http://127.0.0.1" {
		t.Fatalf("target = %q, want http://127.0.0.1", got.Target)
	}
	if calls != 1 {
		t.Fatalf("verify calls = %d, want 1", calls)
	}
}

func TestScanPipelineFanoutAndDedup(t *testing.T) {
	projector := newProjector([]string{"seed"}, projectorOptions{})
	var mu sync.Mutex
	seen := make([]string, 0)

	capabilities := []capability{
		{
			Name:   "service-to-web",
			Accept: acceptsTarget(targetService),
			Worker: 1,
			Run: func(_ context.Context, e event, emit emitFunc) {
				mu.Lock()
				seen = append(seen, "service-to-web")
				mu.Unlock()
				service, ok := e.Target.(serviceTarget)
				if !ok || service.Result == nil {
					return
				}
				emit(targetEvent("test", "", newWebTarget("", service.Result.GetBaseURL(), "")))
			},
		},
		{
			Name:   "web-to-finger",
			Accept: acceptsTarget(targetWeb),
			Worker: 1,
			Run: func(_ context.Context, e event, emit emitFunc) {
				mu.Lock()
				seen = append(seen, "web-to-finger")
				mu.Unlock()
				web, ok := e.Target.(webTarget)
				if !ok {
					return
				}
				emit(findingEvent("test", fingerprintFinding{Target: web.URL, Fingers: []string{"nginx"}}))
			},
		},
	}

	pipeline := newPipeline(context.Background(), capabilities, projector, false)
	result := parsers.NewGOGOResult("127.0.0.1", "80")
	result.Protocol = "http"
	service := targetEvent("test", "", newServiceTarget("", result))
	pipeline.Run([]event{service, service})

	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(seen, []string{"service-to-web", "web-to-finger"}) {
		t.Fatalf("seen capability runs = %#v", seen)
	}
	if len(projector.data.webEndpoints) != 1 {
		t.Fatalf("web endpoints = %d, want 1", len(projector.data.webEndpoints))
	}
	if len(projector.data.fingerprints) != 1 {
		t.Fatalf("fingerprints = %d, want 1", len(projector.data.fingerprints))
	}
	if len(projector.data.gogoResults) != 1 {
		t.Fatalf("gogo results = %d, want 1", len(projector.data.gogoResults))
	}
	if len(projector.data.trace) != 0 {
		t.Fatalf("trace entries = %d, want 0 without debug", len(projector.data.trace))
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

func countTargetKinds(targets []target) map[targetKind]int {
	counts := make(map[targetKind]int)
	for _, target := range targets {
		if target != nil {
			counts[target.Kind()]++
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
	projector := newProjector([]string{"seed"}, projectorOptions{Debug: true})
	capabilities := []capability{
		{
			Name:   "noop",
			Accept: acceptsTarget(targetWeb),
			Worker: 1,
			Run:    func(context.Context, event, emitFunc) {},
		},
	}
	pipeline := newPipeline(context.Background(), capabilities, projector, true)
	pipeline.Run([]event{targetEvent("test", "", newWebTarget("", "http://127.0.0.1", ""))})

	if len(projector.data.trace) == 0 {
		t.Fatal("expected debug trace entries")
	}
	if !strings.Contains(strings.Join(projector.data.trace, "\n"), "dispatch") {
		t.Fatalf("trace missing dispatch entry: %#v", projector.data.trace)
	}
}

func TestScanPipelineCancelReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once

	projector := newProjector([]string{"seed"}, projectorOptions{})
	capabilities := []capability{
		{
			Name:   "wait",
			Accept: acceptsTarget(targetWeb),
			Worker: 1,
			Run: func(ctx context.Context, _ event, _ emitFunc) {
				once.Do(func() { close(started) })
				<-ctx.Done()
			},
		},
	}
	pipeline := newPipeline(ctx, capabilities, projector, false)

	go func() {
		pipeline.Run([]event{targetEvent("test", "", newWebTarget("", "http://127.0.0.1", ""))})
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
	projector := newProjector([]string{"seed"}, projectorOptions{})
	projector.Observe(pipelineEvent{Action: pipelineEventAccept, Event: targetEvent("test", "", newServiceTarget("", parsers.NewGOGOResult("127.0.0.1", "80")))})
	projector.Observe(pipelineEvent{Action: pipelineEventAccept, Event: targetEvent("spray_check", "", newWebProbeTarget("", "spray_check", "", &parsers.SprayResult{
		IsValid:   true,
		UrlString: "http://127.0.0.1:80",
		Status:    401,
		Distance:  1,
	}))})

	out, err := projector.JSONLines()
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			projector := newProjector([]string{"seed"}, projectorOptions{Stream: &buf})
			projector.Observe(pipelineEvent{
				Action: pipelineEventAccept,
				Event:  targetEvent("spray_check", "", newWebProbeTarget("", "spray_check", "", tc.result)),
			})

			if got := buf.String(); got != "" {
				t.Fatalf("stream output = %q, want empty", got)
			}
			if len(projector.data.sprayResults) != 0 {
				t.Fatalf("spray results = %d, want 0", len(projector.data.sprayResults))
			}
			var derived []event
			deriveWebProbeResult(profile{AllowBroadPOC: true}, webProbeResult{
				Source: "spray_check",
				Result: tc.result,
			}, func(event event) {
				derived = append(derived, event)
			})
			if len(derived) != 0 {
				t.Fatalf("derived events = %#v, want none", derived)
			}
		})
	}
}

func TestScanStreamsAcceptedResults(t *testing.T) {
	var buf bytes.Buffer
	projector := newProjector([]string{"seed"}, projectorOptions{Stream: &buf, StreamColor: true})
	result := parsers.NewGOGOResult("127.0.0.1", "80")
	result.Protocol = "http"
	result.Status = "200"

	projector.Observe(pipelineEvent{Action: pipelineEventAccept, Event: targetEvent(capGogoPortscan, "", newServiceTarget("", result))})

	raw := buf.String()
	if !hasANSI(raw) {
		t.Fatalf("colored stream output missing ANSI: %q", raw)
	}
	out := stripANSI(raw)
	if !strings.Contains(out, "[gogo_portscan] http://127.0.0.1:80") {
		t.Fatalf("stream output = %q", out)
	}
	if strings.Contains(out, "##") {
		t.Fatalf("stream output should be single-line event output: %q", out)
	}
}

func TestScanColorizesWebProbeFields(t *testing.T) {
	var buf bytes.Buffer
	projector := newProjector([]string{"seed"}, projectorOptions{Stream: &buf, StreamColor: true})
	projector.Observe(pipelineEvent{Action: pipelineEventAccept, Event: targetEvent("spray_backup", "", newWebProbeTarget("", "spray_backup", "", &parsers.SprayResult{
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
		ansiBold + ansiGreen + "http://127.0.0.1:32768/test.war" + ansiReset,
		ansiCyan + "bak" + ansiReset,
		ansiYellow + "401" + ansiReset,
		ansiYellow + "64" + ansiReset,
		ansiYellow + "26ms" + ansiReset,
		ansiGreen + `"json data"` + ansiReset,
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("colored output missing %q in %q", want, raw)
		}
	}
	out := stripANSI(raw)
	if !strings.Contains(out, `bak 401 64 26ms http://127.0.0.1:32768/test.war "json data"`) {
		t.Fatalf("plain colored output shape changed: %q", out)
	}
	for _, polluted := range []string{"type=", "probe=", "status=", "length=", "time=", "title="} {
		if strings.Contains(out, polluted) {
			t.Fatalf("plain colored output contains key/value pollution %q: %q", polluted, out)
		}
	}
}

func TestScanStreamsWithoutColor(t *testing.T) {
	var buf bytes.Buffer
	projector := newProjector([]string{"seed"}, projectorOptions{Stream: &buf})
	result := parsers.NewGOGOResult("127.0.0.1", "80")
	result.Protocol = "http"
	result.Status = "200"

	projector.Observe(pipelineEvent{Action: pipelineEventAccept, Event: targetEvent(capGogoPortscan, "", newServiceTarget("", result))})

	out := buf.String()
	if hasANSI(out) {
		t.Fatalf("uncolored stream output contains ANSI: %q", out)
	}
	if !strings.Contains(out, "[gogo_portscan] http://127.0.0.1:80") {
		t.Fatalf("stream output = %q", out)
	}
}

func TestProjectorSlowStreamDoesNotHoldStateLock(t *testing.T) {
	writer := &blockingWriter{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	projector := newProjector([]string{"seed"}, projectorOptions{Stream: writer})
	result := parsers.NewGOGOResult("127.0.0.1", "80")
	result.Protocol = "http"
	result.Status = "200"

	observeDone := make(chan struct{})
	go func() {
		projector.Observe(pipelineEvent{Action: pipelineEventAccept, Event: targetEvent(capGogoPortscan, "", newServiceTarget("", result))})
		close(observeDone)
	}()

	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("stream writer was not called")
	}

	jsonDone := make(chan struct{})
	go func() {
		if _, err := projector.JSONLines(); err != nil {
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
	projector := newProjector([]string{"seed"}, projectorOptions{})
	projector.Observe(pipelineEvent{Action: pipelineEventAccept, Event: targetEvent("spray_check", "", newWebProbeTarget("", "spray_check", "", &parsers.SprayResult{
		IsValid:    true,
		UrlString:  "http://127.0.0.1:80",
		Source:     parsers.CheckSource,
		Status:     200,
		BodyLength: 12,
		Distance:   1,
	}))})
	projector.Finish()

	out := projector.PlainText()
	if hasANSI(out) {
		t.Fatalf("plain text output contains ANSI: %q", out)
	}
	if !strings.Contains(out, "check 200 12 http://127.0.0.1:80 1") {
		t.Fatalf("plain text output missing parser content: %q", out)
	}
	if strings.Contains(out, "sim=") || strings.Contains(out, "status=") || strings.Contains(out, "length=") {
		t.Fatalf("plain text output contains key/value pollution: %q", out)
	}
}

func TestScanOutputFileWritesPlainTextWithoutChangingStdout(t *testing.T) {
	cmd := New(&engines.Set{Spray: spray.NewEngine(nil)})
	file := filepath.Join(t.TempDir(), "scan.txt")
	var stream bytes.Buffer

	out, err := cmd.ExecuteStreaming(context.Background(), []string{"-i", "http://127.0.0.1:1", "--mode", "quick", "--timeout", "1", "-f", file}, &stream)
	if err != nil {
		t.Fatalf("ExecuteStreaming() error = %v", err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	fileOut := string(data)
	if hasANSI(fileOut) {
		t.Fatalf("file output contains ANSI: %q", fileOut)
	}
	if !strings.Contains(fileOut, "[scan] completed") {
		t.Fatalf("file output missing summary: %q", fileOut)
	}
	if !strings.Contains(out, "[scan] completed") {
		t.Fatalf("stdout output missing summary: %q", out)
	}
	if strings.Contains(out, "[scan] web ") {
		t.Fatalf("stdout output should not repeat streamed events: %q", out)
	}
	if !strings.Contains(stripANSI(stream.String()), "http://127.0.0.1:1") {
		t.Fatalf("stream output missing event line: %q", stream.String())
	}
	if strings.Contains(stripANSI(stream.String()), "type=web") {
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
	projector := newProjector([]string{"seed"}, projectorOptions{})
	projector.Observe(pipelineEvent{Action: pipelineEventCapabilityStart, Capability: capGogoPortscan, Event: targetEvent("", "", newScanTarget("", "127.0.0.1", ""))})
	projector.Observe(pipelineEvent{Action: pipelineEventAccept, Event: targetEvent(capGogoPortscan, "", newServiceTarget("", parsers.NewGOGOResult("127.0.0.1", "80")))})
	projector.Observe(pipelineEvent{Action: pipelineEventAccept, Event: targetEvent("spray_check", "", newWebProbeTarget("", "spray_check", "", &parsers.SprayResult{
		IsValid:   true,
		UrlString: "http://127.0.0.1:80",
		Status:    200,
		Distance:  1,
	}))})
	projector.Observe(pipelineEvent{Action: pipelineEventAccept, Event: findingEvent(capAgentVerify, verificationFinding{
		OriginalKey:      "vuln|1",
		OriginalKind:     findingVuln,
		OriginalPriority: priorityHigh,
		Status:           verificationConfirmed,
		Target:           "http://127.0.0.1",
		Summary:          "confirmed by test",
	})})
	projector.Finish()

	report := projector.ReportMarkdown()
	if hasANSI(report) {
		t.Fatalf("report contains ANSI: %q", report)
	}
	for _, want := range []string{"# Scan Report", "## Metrics", "## Capability Runs", "## Open Services", "## AI Verification Results", "confirmed by test", "gogo_portscan"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

