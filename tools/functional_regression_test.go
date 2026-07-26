package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	_ "github.com/chainreactors/aiscan/tools/gogo"
	_ "github.com/chainreactors/aiscan/tools/neutron"
	_ "github.com/chainreactors/aiscan/tools/proton"
	"github.com/chainreactors/aiscan/tools/scan/engine"
	_ "github.com/chainreactors/aiscan/tools/spray"
	_ "github.com/chainreactors/aiscan/tools/zombie"
	"github.com/chainreactors/utils/parsers"
)

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
	bus := eventbus.New[output.ToolDataEvent]()
	recorder := newFunctionalRecorder(bus)
	registry := commands.NewRegistry()
	commands.BuildGroup("scanner", &commands.Deps{
		WorkDir:   workDir,
		EngineSet: engineSet,
		Resources: engineSet.Resources,
		DataBus:   bus,
		Logger:    telemetry.NopLogger(),
	}, registry)

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
          - 'AISCAN_REGRESSION_MARKER'
`)

	cases := []functionalCase{
		{
			Name: "gogo/http-fingerprint-jsonl", Tool: "gogo",
			Args:          []string{"-i", host, "-p", port, "-v", "-o", "jl", "-t", "20"},
			SkipUnderRace: true,
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, `"port":"`+port+`"`, "nginx")
				requireEvent(t, result, "gogo", output.ToolDataService, func(data any) bool {
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
			Args:          []string{"-l", targetsFile, "-p", port, "-o", "jl", "-t", "20"},
			SkipUnderRace: true,
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, `"port":"`+port+`"`)
			},
		},
		{
			Name: "spray/fingerprint-json", Tool: "spray",
			Args: []string{"-u", httpServer.URL, "--finger", "-j", "--limit", "5"},
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, httpServer.URL, "nginx")
				requireEvent(t, result, "spray", output.ToolDataWeb, func(data any) bool {
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
				requireEvent(t, result, "spray", output.ToolDataWeb, func(data any) bool {
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
				requireOutputContains(t, result, `"matched":true`, `"template":"regression-marker"`)
				requireEvent(t, result, "neutron", output.ToolDataVuln, nil)
			},
		},
		{
			Name: "proton/file-secret-json", Tool: "proton",
			Args: []string{"-i", secretFile, "-j"},
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, "AKIAIOSFODNN7EXAMPLE")
				requireEvent(t, result, "proton", output.ToolDataVuln, nil)
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
			Args:          []string{"-i", host, "--ports", port, "--mode", "quick", "--verify=off", "--timeout", "2", "--no-color"},
			Timeout:       30 * time.Second,
			SkipUnderRace: true,
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, "[summary] completed", port)
				requireEvent(t, result, "gogo", output.ToolDataService, nil)
			},
		},
	}

	// The full-tag suite supplies the katana and passive cases.
	requireFunctionalCoverage(t, registry, cases, "katana", "passive")
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
		fmt.Fprint(w, `<html><head><title>AIScan Regression Lab</title></head><body><a href="/admin">admin</a><script src="/app.js"></script></body></html>`)
	})
	mux.HandleFunc("/admin", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "nginx/1.25.4")
		fmt.Fprint(w, "AISCAN_ADMIN_ENDPOINT")
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
		fmt.Fprint(w, "AISCAN_REGRESSION_MARKER")
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
