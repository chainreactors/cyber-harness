package scanner

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
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	protonext "github.com/chainreactors/cyber/pkg/exts/proton"
	searchext "github.com/chainreactors/cyber/pkg/exts/search"
	subagentext "github.com/chainreactors/cyber/pkg/exts/subagent"
	"github.com/chainreactors/cyber/pkg/harness"
	"github.com/chainreactors/cyber/tools/resources"
	"github.com/chainreactors/utils/parsers"
)

type scannerInstallation struct {
	commands *coretool.CommandRegistry
	events   *coreevents.Stream
	prompts  prompt.Resolver
}

func installScanner(t *testing.T, directory string, config Config, extra ...extension.Extension) scannerInstallation {
	t.Helper()
	values, err := harness.BaseExtensions(harness.BaseConfig{Directory: directory, Provider: provider.StartupConfig{Mode: provider.StartupDisabled}})
	if err != nil {
		t.Fatal(err)
	}
	values = append(values, loopext.New(agent.StandardLoop{}), subagentext.New(), New(config, directory), protonext.New(protonext.Config{Directory: directory}), searchext.New(searchext.Options{}))
	values = append(values, extra...)
	var installed scannerInstallation
	values = append(values, extension.Func{LoadFunc: func(scope *extension.Scope) error {
		commands, err := extension.Use[coretool.CommandExecutor](scope)
		if err != nil {
			return err
		}
		installed.commands = commands.(*coretool.CommandRegistry)
		if installed.events, err = extension.Use[*coreevents.Stream](scope); err != nil {
			return err
		}
		installed.prompts, err = extension.Use[prompt.Resolver](scope)
		return err
	}})
	hosttest.Load(t, t.Context(), values...)
	return installed
}

func TestScannerMissingResourcesDoesNotInventEngines(t *testing.T) {
	installed := installScanner(t, t.TempDir(), Config{Resources: resources.Options{Mode: "invalid"}})
	for _, name := range []string{"neutron", "gogo", "spray", "zombie", "scan"} {
		if installed.commands.Has(name) {
			t.Fatalf("unexpected unavailable command %s", name)
		}
	}
}
func TestCommandsCarryAPromptDescription(t *testing.T) {
	reg := installScanner(t, t.TempDir(), Config{}).commands
	docs := reg.UsageDocs()
	for _, name := range Names() {
		if reg.Has(name) && strings.Contains(docs, "- "+name+"\n") {
			t.Errorf("%s has no prompt description", name)
		}
	}
}
func TestScannerAndSearchContributeTheirCommands(t *testing.T) {
	reg := installScanner(t, t.TempDir(), Config{}).commands
	for _, name := range []string{"cyberhub", "fetch"} {
		if !reg.Has(name) {
			t.Fatalf("missing %s", name)
		}
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
	bus.Observe(func(event *aop.Event) {
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
	})
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

func runFunctionalCases(t *testing.T, registry *coretool.CommandRegistry, recorder *functionalRecorder, cases []functionalCase) {
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
			parent := &coretool.Execution{
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

func requireFunctionalCoverage(t *testing.T, registry *coretool.CommandRegistry, cases []functionalCase, coveredElsewhere ...string) {
	t.Helper()
	covered := make(map[string]bool, len(cases)+len(coveredElsewhere))
	for _, testCase := range cases {
		covered[testCase.Tool] = true
	}
	for _, name := range coveredElsewhere {
		covered[name] = true
	}
	for _, name := range Names() {
		if !registry.Has(name) {
			continue
		}
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

	workDir := t.TempDir()
	installed := installScanner(t, workDir, Config{})
	recorder := newFunctionalRecorder(installed.events)
	registry := installed.commands

	required := []string{"scan", "gogo", "spray", "zombie", "neutron", "proton"}
	for _, name := range required {
		if !registry.Has(name) {
			t.Fatalf("scanner registry missing %q; registered=%v", name, registry.Names())
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
