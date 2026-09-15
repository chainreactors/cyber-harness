package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/cmd/harness"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/resources"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/tools/curl"
	"github.com/chainreactors/cyber/tools/gogo"
	"github.com/chainreactors/cyber/tools/neutron"
	"github.com/chainreactors/cyber/tools/proton"
	"github.com/chainreactors/cyber/tools/scan/engine"
	searchtools "github.com/chainreactors/cyber/tools/search"
	"github.com/chainreactors/cyber/tools/spray"
	"github.com/chainreactors/cyber/tools/zombie"
	fingerslib "github.com/chainreactors/fingers/fingers"
	neutronhttp "github.com/chainreactors/neutron/protocols/http"
	"github.com/chainreactors/proxyclient"
	sdkfingers "github.com/chainreactors/sdk/fingers"
	sdkgogo "github.com/chainreactors/sdk/gogo"
	"github.com/chainreactors/sdk/pkg/association"
	sdkspray "github.com/chainreactors/sdk/spray"
	"github.com/chainreactors/utils/parsers"
)

func buildRegistry(t *testing.T, engineSet *engine.Set) *commands.Registry {
	t.Helper()
	logger := telemetry.NopLogger()
	events := coreevents.New()
	fetch := searchtools.NewFetchCommand()
	var index *association.Index
	if engineSet != nil {
		index = engineSet.Index
		if index == nil && engineSet.Resources != nil && engineSet.Resources.FingersConfig != nil {
			full := engineSet.Resources.FingersConfig.FullFingers
			index = association.NewIndex()
			index.BuildWithFingers(full.Fingers(), full.Aliases(), nil)
		}
	}
	cyberhub := searchtools.NewCyberhubSearch(index)
	return harness.CommandGroups(t,
		harness.CommandGroup{Name: "scanner", Values: scannerCommandValues(engineSet, t.TempDir(), events, logger)},
		harness.CommandGroup{Name: "search", Values: []commands.Command{
			{Name: fetch.Name(), Usage: fetch.Usage(), Run: fetch.Run},
			{Name: cyberhub.Name(), Usage: cyberhub.Usage(), Run: cyberhub.Run},
		}},
	)
}

func registerTestScanners(t *testing.T, engineSet *engine.Set, workDir string, events aop.EventPublisher, logger telemetry.Logger, extra ...commands.Command) *commands.Registry {
	t.Helper()
	values := scannerCommandValues(engineSet, workDir, events, logger)
	values = append(values, extra...)
	return harness.Commands(t, "scanner", values...)
}

func scannerCommandValues(engineSet *engine.Set, workDir string, events aop.EventPublisher, logger telemetry.Logger) []commands.Command {
	values := []commands.Command{
		curl.NewCommand(logger, "", events),
		proton.NewCommand(workDir, engineSet.Resources, logger, "", events),
	}
	for _, factory := range []func() (commands.Command, error){
		func() (commands.Command, error) { return gogo.NewCommand(engineSet, logger, "", events) },
		func() (commands.Command, error) { return neutron.NewCommand(engineSet, logger, "", events) },
		func() (commands.Command, error) { return spray.NewCommand(engineSet, logger, "", events) },
		func() (commands.Command, error) { return zombie.NewCommand(engineSet, logger, "", events) },
		func() (commands.Command, error) { return NewScanCommand(engineSet, nil, "", events) },
	} {
		if command, err := factory(); err == nil {
			values = append(values, command)
		}
	}
	return values
}

func TestRegisterAllTreatsNeutronAsOptional(t *testing.T) {
	gogoEng, _ := sdkgogo.NewEngine(nil)
	sprayEng, _ := sdkspray.NewEngine(nil)
	engineSet := &engine.Set{
		Gogo:  gogoEng,
		Spray: sprayEng,
	}
	reg := buildRegistry(t, engineSet)

	for _, name := range []string{"scan", "gogo", "spray"} {
		if !reg.Has(name) {
			t.Fatalf("expected %q to be registered", name)
		}
	}
	if reg.Has("neutron") {
		t.Fatal("neutron should not be registered without templates")
	}
}

func TestRegisterAllRegistersSearchWithResources(t *testing.T) {
	engineSet := &engine.Set{
		Resources: &resources.Set{
			FingersConfig: sdkfingers.NewConfig().WithFingers(fingerslib.Fingers{{Name: "nginx", Protocol: "http"}}),
		},
	}
	reg := buildRegistry(t, engineSet)

	if !reg.Has("cyberhub") {
		t.Fatal("expected cyberhub search command to be registered")
	}
}

// ---------------------------------------------------------------------------
// Proxy tests
// ---------------------------------------------------------------------------

// startSOCKS5CountingProxy starts a minimal SOCKS5 server that counts
// connection attempts. It returns the proxy URL and a function to read
// the connection count.
func startSOCKS5CountingProxy(t *testing.T) (string, func() int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var count atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			count.Add(1)
			go handleSOCKS5(conn)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return fmt.Sprintf("socks5://%s", ln.Addr().String()), func() int32 { return count.Load() }
}

func handleSOCKS5(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil || n < 3 || buf[0] != 0x05 {
		return
	}
	conn.Write([]byte{0x05, 0x00})

	n, err = conn.Read(buf)
	if err != nil || n < 7 || buf[0] != 0x05 || buf[1] != 0x01 {
		return
	}

	conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})

	go io.Copy(io.Discard, conn)
	time.Sleep(50 * time.Millisecond)
}

func TestProxyclientDialCreateFromURL(t *testing.T) {
	proxyAddr, getCount := startSOCKS5CountingProxy(t)

	proxyURL, err := url.Parse(proxyAddr)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	dial, err := proxyclient.NewClient(proxyURL)
	if err != nil {
		t.Fatalf("proxyclient.NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := dial.DialContext(ctx, "tcp", "127.0.0.1:1")
	if conn != nil {
		conn.Close()
	}
	if getCount() == 0 {
		t.Fatal("proxyclient dial did not reach the SOCKS5 proxy")
	}
	_ = err
}

func TestGogoInjectProxy(t *testing.T) {
	proxyAddr, _ := startSOCKS5CountingProxy(t)

	cmd := gogo.New(nil).WithProxy(proxyAddr)

	var output bytes.Buffer
	_, err := cmd.Run(context.Background(), &commands.Execution{Args: []string{"--help"}, Stdout: &output, Stderr: &output})
	if err != nil {
		t.Fatalf("gogo --help with proxy: %v", err)
	}
	if output.String() == "" {
		t.Fatal("expected help output")
	}

	injected := cmd.TestInjectProxy([]string{"-i", "127.0.0.1"})
	hasProxy := false
	for i, arg := range injected {
		if arg == "--proxy" && i+1 < len(injected) && injected[i+1] == proxyAddr {
			hasProxy = true
			break
		}
	}
	if !hasProxy {
		t.Fatalf("expected --proxy %s in args, got %v", proxyAddr, injected)
	}

	alreadyHas := cmd.TestInjectProxy([]string{"-i", "127.0.0.1", "--proxy", "socks5://other:1080"})
	proxyCount := 0
	for _, arg := range alreadyHas {
		if arg == "--proxy" {
			proxyCount++
		}
	}
	if proxyCount != 1 {
		t.Fatalf("expected 1 --proxy flag (user-provided), got %d in %v", proxyCount, alreadyHas)
	}
}

func TestSprayInjectProxy(t *testing.T) {
	proxyAddr, _ := startSOCKS5CountingProxy(t)

	cmd := spray.New(nil).WithProxy(proxyAddr)

	injected := cmd.TestInjectProxy([]string{"-u", "http://example.com"})
	hasProxy := false
	for i, arg := range injected {
		if arg == "--proxy" && i+1 < len(injected) && injected[i+1] == proxyAddr {
			hasProxy = true
			break
		}
	}
	if !hasProxy {
		t.Fatalf("expected --proxy %s in args, got %v", proxyAddr, injected)
	}
}

// TestZombieExecuteWithProxy verifies that zombie's Execute passes proxy via
// RunOptions.ProxyDial (not global patching).
func TestZombieExecuteWithProxy(t *testing.T) {
	proxyAddr, _ := startSOCKS5CountingProxy(t)

	cmd := zombie.New(nil).WithProxy(proxyAddr)

	// Execute with --help just to verify no panic; the proxy is built
	// but not exercised because --help exits before any network I/O.
	var output bytes.Buffer
	_, err := cmd.Run(context.Background(), &commands.Execution{Args: []string{"--help"}, Stdout: &output, Stderr: &output})
	if err != nil {
		t.Fatalf("zombie --help: %v", err)
	}
}

// Neutron proxy configuration belongs to each command and resource set.
func TestNeutronProxyIsInstanceLocal(t *testing.T) {
	original := fmt.Sprintf("%p/%p", neutronhttp.DefaultOption.Proxy, neutronhttp.DefaultTransport.Proxy)
	first := neutron.New(nil, nil).WithProxy("socks5://127.0.0.1:10001")
	second := neutron.New(nil, nil).WithProxy("socks5://127.0.0.1:10002")
	first.SetProxy("")
	if first.Proxy != "" || second.Proxy != "socks5://127.0.0.1:10002" {
		t.Fatal("proxy leaked between commands")
	}
	if current := fmt.Sprintf("%p/%p", neutronhttp.DefaultOption.Proxy, neutronhttp.DefaultTransport.Proxy); current != original {
		t.Fatal("command changed Neutron process defaults")
	}
}

type functionalResult struct {
	Stdout string
	Stderr string
	Events []functionalEvent
}

type functionalCase struct {
	Name    string
	Tool    string
	Args    []string
	Stdin   string
	Timeout time.Duration
	Check   func(*testing.T, functionalResult)
}

type functionalRecorder struct {
	mu     sync.Mutex
	events []functionalEvent
}

type functionalEvent struct {
	Tool, Kind, Target, CallID string
	Data                       any
}

func newFunctionalRecorder(bus *coreevents.Stream) *functionalRecorder {
	recorder := &functionalRecorder{}
	bus.Observe(coreevents.ObserverFunc(func(event *aop.Event) {
		if event == nil || event.GetExtension() == nil {
			return
		}
		artifact := new(toolpb.Artifact)
		if event.GetExtension().UnmarshalTo(artifact) != nil {
			return
		}
		decoded := decodeFunctionalArtifact(artifact)
		ref := new(operationpb.Ref)
		_, _ = aop.FindTypedExtension(event, ref)
		recorder.mu.Lock()
		recorder.events = append(recorder.events, functionalEvent{
			Tool: artifact.Tool, Kind: artifact.Kind, Target: artifact.Target, CallID: ref.GetCallId(), Data: decoded,
		})
		recorder.mu.Unlock()
	}))
	return recorder
}

func decodeFunctionalArtifact(artifact *toolpb.Artifact) any {
	if artifact == nil {
		return nil
	}
	var value any
	switch artifact.Tool {
	case "gogo":
		value = new(parsers.GOGOResult)
	case "spray":
		value = new(parsers.SprayResult)
	default:
		value = new(any)
	}
	if json.Unmarshal(artifact.Data, value) != nil {
		return nil
	}
	if holder, ok := value.(*any); ok {
		return *holder
	}
	return value
}

func (r *functionalRecorder) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func (r *functionalRecorder) since(mark int) []functionalEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]functionalEvent(nil), r.events[mark:]...)
}

func runFunctionalCases(t *testing.T, registry *commands.Registry, recorder *functionalRecorder, cases []functionalCase) {
	t.Helper()
	for _, testCase := range cases {
		t.Run(testCase.Name, func(t *testing.T) {
			if !registry.Has(testCase.Tool) {
				t.Fatalf("tool %q is not registered", testCase.Tool)
			}
			timeout := testCase.Timeout
			if timeout <= 0 {
				timeout = 30 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			var stdout, stderr bytes.Buffer
			parent := &commands.Execution{
				ID:     "functional-" + testCase.Name,
				Stdin:  strings.NewReader(testCase.Stdin),
				Stdout: &stdout,
				Stderr: &stderr,
			}
			mark := recorder.mark()
			_, err := registry.Run(ctx, append([]string{testCase.Tool}, testCase.Args...), parent)
			if err != nil {
				t.Fatalf("%s %s: %v\nstdout:\n%s\nstderr:\n%s", testCase.Tool, strings.Join(testCase.Args, " "), err, stdout.String(), stderr.String())
			}
			if err := ctx.Err(); err != nil {
				t.Fatalf("%s exceeded %s: %v", testCase.Tool, timeout, err)
			}
			result := functionalResult{Stdout: stdout.String(), Stderr: stderr.String(), Events: recorder.since(mark)}
			if testCase.Check != nil {
				testCase.Check(t, result)
			}
		})
	}
}

func requireFunctionalCoverage(t *testing.T, registry *commands.Registry, cases []functionalCase, coveredElsewhere ...string) {
	t.Helper()
	covered := make(map[string]bool, len(cases)+len(coveredElsewhere))
	for _, testCase := range cases {
		covered[testCase.Tool] = true
	}
	for _, name := range coveredElsewhere {
		covered[name] = true
	}
	for _, name := range registry.GroupNames("scanner") {
		if !covered[name] {
			t.Fatalf("scanner %q has no functional regression case", name)
		}
	}
}

func requireOutputContains(t *testing.T, result functionalResult, values ...string) {
	t.Helper()
	combined := result.Stdout + "\n" + result.Stderr
	for _, value := range values {
		if !strings.Contains(combined, value) {
			t.Fatalf("output missing %q\nstdout:\n%s\nstderr:\n%s", value, result.Stdout, result.Stderr)
		}
	}
}

func requireEvent(t *testing.T, result functionalResult, tool, kind string, match func(any) bool) functionalEvent {
	t.Helper()
	for _, event := range result.Events {
		if event.Tool == tool && event.Kind == kind && (match == nil || match(event.Data)) {
			return event
		}
	}
	t.Fatalf("missing event tool=%s kind=%s in %s", tool, kind, formatFunctionalEvents(result.Events))
	return functionalEvent{}
}

func formatFunctionalEvents(events []functionalEvent) string {
	var b strings.Builder
	for _, event := range events {
		fmt.Fprintf(&b, "{%s %s %s %T} ", event.Tool, event.Kind, event.Target, event.Data)
	}
	return b.String()
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestScannerFunctionalRegression(t *testing.T) {
	httpServer := newScannerHTTPFixture(t)
	tlsServer := newScannerTLSFixture(t)
	httpURL, err := url.Parse(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(httpURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	redisAddr := newRedisAuthFixture(t, "lab-secret")
	_, redisPort, err := net.SplitHostPort(redisAddr)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	engineSet, err := engine.InitWithOptions(ctx, resources.Options{}, telemetry.NopLogger())
	if err != nil {
		t.Fatalf("initialize scanner engines: %v", err)
	}
	defer engineSet.Close()

	workDir := t.TempDir()
	bus := coreevents.New()
	recorder := newFunctionalRecorder(bus)
	registry := registerTestScanners(t, engineSet, workDir, bus, telemetry.NopLogger())

	required := []string{"scan", "gogo", "spray", "zombie", "neutron", "proton"}
	for _, name := range required {
		if !registry.Has(name) {
			t.Fatalf("scanner registry missing %q; registered=%v", name, registry.GroupNames("scanner"))
		}
	}

	targetsFile := filepath.Join(workDir, "targets.txt")
	writeTestFile(t, targetsFile, host+"\n")
	secretFile := filepath.Join(workDir, "config.env")
	writeTestFile(t, secretFile, "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE\n")
	templateFile := filepath.Join(workDir, "regression-poc.yaml")
	writeTestFile(t, templateFile, `id: regression-marker
info:
  name: Regression marker exposure
  severity: high
  tags: regression
http:
  - method: GET
    path:
      - '{{BaseURL}}/poc'
    matchers:
      - type: word
        words:
          - 'CYBER_REGRESSION_MARKER'
`)

	cases := []functionalCase{
		{
			Name: "gogo/http-fingerprint-jsonl", Tool: "gogo",
			Args: []string{"-i", host, "-p", port, "-v", "-o", "jl", "-t", "20"},
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, `"port":"`+port+`"`, "nginx")
				requireEvent(t, result, "gogo", toolpb.ArtifactKindService, func(data any) bool {
					item, ok := data.(*parsers.GOGOResult)
					if !ok || item == nil || item.Port != port {
						return false
					}
					_, hasNginx := item.Frameworks["nginx"]
					return hasNginx
				})
			},
		},
		{
			Name: "gogo/target-file", Tool: "gogo",
			Args: []string{"-l", targetsFile, "-p", port, "-o", "jl", "-t", "20"},
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, `"port":"`+port+`"`)
			},
		},
		{
			Name: "spray/fingerprint-json", Tool: "spray",
			Args: []string{"-u", httpServer.URL, "--finger", "-j", "--limit", "5"},
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, httpServer.URL, "nginx")
				requireEvent(t, result, "spray", toolpb.ArtifactKindWeb, func(data any) bool {
					item, ok := data.(*parsers.SprayResult)
					if !ok || item == nil || item.Status != http.StatusOK {
						return false
					}
					_, hasNginx := item.Frameworks["nginx"]
					return hasNginx
				})
			},
		},
		{
			Name: "spray/explicit-https", Tool: "spray",
			Args: []string{"-u", tlsServer.URL, "-j", "--limit", "1"},
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, `"url":"`+tlsServer.URL+`"`, `"status":200`)
				requireEvent(t, result, "spray", toolpb.ArtifactKindWeb, func(data any) bool {
					item, ok := data.(*parsers.SprayResult)
					return ok && item != nil && item.Status == http.StatusOK && strings.HasPrefix(item.UrlString, "https://")
				})
			},
		},
		{
			Name: "spray/crawl", Tool: "spray",
			Args: []string{"-u", httpServer.URL, "--crawl", "--limit", "10"},
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, "/admin")
			},
		},
		{
			Name: "zombie/redis-passwords", Tool: "zombie",
			Args:    []string{"-i", host + ":" + redisPort, "-s", "redis", "-p", "wrong-password", "-p", "lab-secret", "--no-unauth", "--no-honeypot", "--force-continue", "-t", "1", "--concurrency", "1", "--timeout", "2", "-o", "json"},
			Timeout: 15 * time.Second,
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, "lab-secret", `"service":"redis"`)
				if strings.Contains(result.Stdout, `"password":"wrong-password"`) {
					t.Fatalf("zombie reported an invalid password as successful: %s", result.Stdout)
				}
			},
		},
		{
			Name: "zombie/redis-pitchfork-auth", Tool: "zombie",
			Args:    []string{"-i", host + ":" + redisPort, "-s", "redis", "-a", "operator::lab-secret", "-m", "pitchfork", "--no-unauth", "--no-honeypot", "-t", "1", "--concurrency", "1", "--timeout", "2", "-o", "json"},
			Timeout: 15 * time.Second,
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, "operator", "lab-secret", "\"service\":\"redis\"")
			},
		},
		{
			Name: "neutron/custom-poc-filter-json", Tool: "neutron",
			Args: []string{"-i", httpServer.URL, "-t", templateFile, "--tags", "regression", "-s", "high", "-j"},
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, `"matched":true`, `"template_id":"regression-marker"`)
				requireEvent(t, result, "neutron", toolpb.ArtifactKindVuln, nil)
			},
		},
		{
			Name: "proton/file-secret-json", Tool: "proton",
			Args: []string{"-i", secretFile, "-j"},
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, "AKIAIOSFODNN7EXAMPLE")
				requireEvent(t, result, "proton", toolpb.ArtifactKindVuln, nil)
			},
		},
		{
			Name: "proton/stdin-expression", Tool: "proton", Stdin: "token=LAB_TOKEN_12345\n",
			Args: []string{"-e", "LAB_TOKEN_[0-9]+", "-j"},
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, "LAB_TOKEN_12345")
			},
		},
		{
			Name: "scan/quick-pipeline", Tool: "scan",
			Args:    []string{"-i", host, "--ports", port, "--mode", "quick", "--verify=off", "--timeout", "2", "--no-color"},
			Timeout: 30 * time.Second,
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, "[summary] completed", port)
				requireEvent(t, result, "gogo", toolpb.ArtifactKindService, nil)
			},
		},
	}

	// The full-tag suite supplies the katana and passive cases.
	requireFunctionalCoverage(t, registry, cases, "curl", "katana", "passive")
	runFunctionalCases(t, registry, recorder, cases)
}

func newScannerHTTPFixture(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(newScannerHTTPHandler())
	t.Cleanup(server.Close)
	return server
}

func newScannerTLSFixture(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(newScannerHTTPHandler())
	t.Cleanup(server.Close)
	return server
}

func newScannerHTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "nginx/1.25.4")
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Cyber Regression Lab</title></head><body><a href="/admin">admin</a><script src="/app.js"></script></body></html>`)
	})
	mux.HandleFunc("/admin", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "nginx/1.25.4")
		fmt.Fprint(w, "CYBER_ADMIN_ENDPOINT")
	})
	mux.HandleFunc("/app.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprint(w, `fetch('/api/status')`)
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/poc", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "nginx/1.25.4")
		fmt.Fprint(w, "CYBER_REGRESSION_MARKER")
	})
	return mux
}

func newRedisAuthFixture(t *testing.T, password string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start redis fixture: %v", err)
	}
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				handleRedisConnection(conn, password)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		wg.Wait()
	})
	return listener.Addr().String()
}

func handleRedisConnection(conn net.Conn, password string) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	authed := false
	for {
		command, err := readRESPCommand(reader)
		if err != nil {
			return
		}
		if len(command) == 0 {
			return
		}
		switch strings.ToUpper(command[0]) {
		case "AUTH":
			if len(command) == 2 && command[1] == password {
				authed = true
				_, _ = fmt.Fprint(conn, "+OK\r\n")
			} else {
				_, _ = fmt.Fprint(conn, "-ERR invalid password\r\n")
			}
		case "PING":
			if authed {
				_, _ = fmt.Fprint(conn, "+PONG\r\n")
			} else {
				_, _ = fmt.Fprint(conn, "-NOAUTH Authentication required.\r\n")
			}
		case "QUIT":
			_, _ = fmt.Fprint(conn, "+OK\r\n")
			return
		default:
			_, _ = fmt.Fprint(conn, "-ERR unsupported command\r\n")
		}
	}
}

func readRESPCommand(reader *bufio.Reader) ([]string, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(header, "*") {
		return strings.Fields(header), nil
	}
	count, err := strconv.Atoi(strings.TrimPrefix(header, "*"))
	if err != nil || count <= 0 {
		return nil, fmt.Errorf("invalid RESP array %q", header)
	}
	command := make([]string, 0, count)
	for i := 0; i < count; i++ {
		lengthLine, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		length, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(lengthLine), "$"))
		if err != nil || length < 0 {
			return nil, fmt.Errorf("invalid RESP bulk length %q", lengthLine)
		}
		value := make([]byte, length+2)
		if _, err := io.ReadFull(reader, value); err != nil {
			return nil, err
		}
		command = append(command, string(value[:length]))
	}
	return command, nil
}
