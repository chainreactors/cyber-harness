package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/core/resource"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
)

const schema = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"address":{"type":"integer"}},"anyOf":[{"required":["address"]}],"unevaluatedProperties":false}`
const richResult = `{"content":[{"type":"text","text":"decompiled"},{"type":"image","data":"AQID","mimeType":"image/png","annotations":{"audience":["user"]}},{"type":"audio","data":"BAU=","mimeType":"audio/wav"},{"type":"resource_link","uri":"file:///target.asm","name":"assembly","mimeType":"text/plain"},{"type":"resource","resource":{"uri":"re://f/1","text":"function","mimeType":"text/plain"}},{"type":"resource","resource":{"uri":"re://blob","blob":"Bg==","mimeType":"application/octet-stream"}},{"type":"future","original":9007199254740993}],"structuredContent":{"address":9007199254740993},"_meta":{"source":"idb"},"isError":true}`

func TestHTTPDiscoveryAndInvocationPreserveWireValues(t *testing.T) {
	var calls, deletes atomic.Int32
	var received atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("authentication missing, including on session cleanup")
		}
		if r.Method == http.MethodDelete {
			deletes.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var result string
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "owned-session")
			result = `{"protocolVersion":"2025-03-26","capabilities":{"tools":{}},"serverInfo":{"name":"test","version":"1"}}`
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			if strings.Contains(string(request.Params), "cursor") {
				result = `{"tools":[{"name":"other","inputSchema":{"type":"object"}}]}`
			} else {
				result = `{"tools":[{"name":"decompile","description":"Read code","inputSchema":` + schema + `}],"nextCursor":"page-2"}`
			}
		case "tools/call":
			calls.Add(1)
			received.Store(string(request.Params))
			result = richResult
		default:
			t.Errorf("unexpected request %s", request.Method)
			return
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, request.ID, result)
	}))
	defer upstream.Close()
	c := openHTTP(t, ServerConfig{URL: upstream.URL, Headers: map[string]string{"Authorization": "Bearer test-secret"}, Tools: []string{"decompile"}})
	catalog, err := c.Discover(context.Background())
	if err != nil || len(catalog.tools) != 1 {
		t.Fatalf("discover: %v, %v", catalog, err)
	}
	if string(catalog.tools[0].InputSchema) != schema {
		t.Fatalf("definition lost source schema: %v", catalog.tools[0])
	}
	for _, invalid := range []string{"", "null", "[]", "true", "{} {}", "{broken}"} {
		if _, err := catalog.Call(context.Background(), "decompile", json.RawMessage(invalid)); err == nil {
			t.Fatalf("accepted invalid arguments: %q", invalid)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid arguments dispatched")
	}
	result, err := catalog.Call(context.Background(), "decompile", json.RawMessage(`{"address":9007199254740993}`))
	if err == nil || !strings.Contains(err.Error(), "isError") {
		t.Fatalf("call: %v, %v", result, err)
	}
	if !strings.Contains(received.Load().(string), `"arguments":{"address":9007199254740993}`) || !strings.Contains(received.Load().(string), `"name":"decompile"`) {
		t.Fatalf("upstream arguments/name changed: %s", received.Load())
	}
	if string(result) != richResult {
		t.Fatal("raw result lost annotations, structured content or metadata")
	}
	var output bytes.Buffer
	command := catalog.Command()
	_, err = command.Run(context.Background(), &coretool.Execution{Args: []string{"decompile", "--address", "9007199254740993"}, Stdout: &output})
	if err == nil || output.String() != richResult+"\n" {
		t.Fatalf("CLI did not preserve complete failed result: %s %v", &output, err)
	}
	before := calls.Load()
	for _, args := range [][]string{
		{"other"}, {"decompile", "--json", "{}", "--file", "x"},
		{"decompile", "--json"}, {"decompile", "--json", "[]"},
		{"--list", "unexpected"}, {"schema"}, {"unknown"}, {"--"},
	} {
		if _, err := command.Run(context.Background(), &coretool.Execution{Args: args, Stdout: &output}); err == nil {
			t.Fatalf("invalid CLI input accepted: %q", args)
		}
	}
	if calls.Load() != before {
		t.Fatal("invalid or excluded CLI call reached upstream")
	}
	output.Reset()
	if _, err := command.Run(context.Background(), &coretool.Execution{Args: []string{"decompile", "--help"}, Stdout: &output}); err != nil || !strings.Contains(output.String(), schema) || !strings.Contains(output.String(), "--address <integer>") {
		t.Fatalf("generated tool help lost schema: %s %v", &output, err)
	}
	if err := c.Close(context.Background()); err != nil || deletes.Load() != 1 {
		t.Fatalf("close: %v, deletes=%d", err, deletes.Load())
	}
}

func openHTTP(t *testing.T, config ServerConfig) *Connection {
	t.Helper()
	c, err := New("ida", config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := c.Start(context.Background(), context.Background()); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCancellationAndErrorDiagnostics(t *testing.T) {
	canceled := make(chan struct{}, 1)
	entered := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-03-26","capabilities":{"tools":{}},"serverInfo":{"name":"test","version":"1"}}}`, req.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case cancellationMethod:
			canceled <- struct{}{}
			w.WriteHeader(http.StatusAccepted)
		case "wait":
			entered <- struct{}{}
			<-r.Context().Done()
		case "fail":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32001,"message":"Bearer secret-value rejected","data":{"reason":"unknown database"}}}`, req.ID)
		}
	}))
	defer upstream.Close()
	c := openHTTP(t, ServerConfig{URL: upstream.URL, Headers: map[string]string{"Authorization": "Bearer secret-value"}})
	_, err := c.request(context.Background(), "fail", nil)
	var rpc *RPCError
	if !errors.As(err, &rpc) || rpc.Details.Code != -32001 || !strings.Contains(err.Error(), "unknown database") || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("protocol failure lost or credentials exposed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { _, err := c.request(ctx, "wait", nil); finished <- err }()
	<-entered
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request did not respect cancellation")
	}
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream cancellation notification missing")
	}
}

type blockingClose struct {
	transport.Interface
	entered chan struct{}
	release chan struct{}
}

func (b *blockingClose) Close() error { close(b.entered); <-b.release; return nil }

func TestCloseDeadlineRetainsCleanupForRetry(t *testing.T) {
	b := &blockingClose{entered: make(chan struct{}), release: make(chan struct{})}
	c := &Connection{name: "test", client: client.NewClient(b), started: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Close(ctx); !errors.Is(err, resource.ErrCloseIncomplete) {
		t.Fatalf("close must retain incomplete cleanup: %v", err)
	}
	<-b.entered
	close(b.release)
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConfigurationAndAllowlistOmission(t *testing.T) {
	for _, config := range []ServerConfig{{}, {Command: "python", URL: "http://localhost"}, {URL: "file:///tmp/socket"}, {URL: "http://user:pass@localhost"}, {URL: "http://localhost", Cwd: "x"}, {Command: "python", TimeoutSeconds: -1}} {
		if config.Validate("ida") == nil {
			t.Fatalf("accepted invalid config: %#v", config)
		}
	}
	config := ServerConfig{Command: "python", Tools: []string{}, Env: map[string]string{"KEY": "value"}}
	copy := config.Clone()
	copy.Env["KEY"] = "different"
	if copy.Tools == nil || config.Env["KEY"] != "value" {
		t.Fatal("clone lost empty allowlist or shared mutable environment")
	}
	encoded, err := json.Marshal(copy)
	if err != nil || !strings.Contains(string(encoded), `"tools":[]`) {
		t.Fatalf("serialization lost empty allowlist: %s, %v", encoded, err)
	}
	if err := (ServerConfig{Command: "python", Tools: []string{strings.Repeat("a", 100), "tool.with.dots", "schema"}}).Validate("ida"); err != nil {
		t.Fatalf("upstream names should not be restricted by function naming: %v", err)
	}
}

func TestMalformedCatalogAndResponseFailures(t *testing.T) {
	for name, catalog := range map[string]string{
		"missing tools":   `{"nextCursor":"page"}`,
		"null tools":      `{"tools":null}`,
		"duplicate names": `{"tools":[{"name":"echo","inputSchema":{"type":"object"}},{"name":"echo","inputSchema":{"type":"object"}}]}`,
		"schema array":    `{"tools":[{"name":"echo","inputSchema":[]}]}`,
		"repeated cursor": `{"tools":[],"nextCursor":"same"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var pages atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				w.Header().Set("Content-Type", "application/json")
				var result string
				switch req.Method {
				case "initialize":
					result = `{"protocolVersion":"2025-03-26","capabilities":{"tools":{}},"serverInfo":{"name":"test","version":"1"}}`
				case "notifications/initialized":
					w.WriteHeader(http.StatusAccepted)
					return
				case "tools/list":
					pages.Add(1)
					result = catalog
				case "mismatch":
					req.ID = json.RawMessage(`"other-id"`)
					result = `{}`
				}
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, req.ID, result)
			}))
			defer upstream.Close()
			c := openHTTP(t, ServerConfig{URL: upstream.URL})
			if _, err := c.Discover(context.Background()); err == nil {
				t.Fatal("malformed catalog accepted")
			}
			if pages.Load() > 2 {
				t.Fatal("looping catalog was not stopped")
			}
			if _, err := c.request(context.Background(), "mismatch", nil); err == nil || !strings.Contains(err.Error(), "mismatched") {
				t.Fatalf("response identity not checked: %v", err)
			}
		})
	}
}

func TestHTTPDoesNotFollowRedirectOrReplay(t *testing.T) {
	var forwarded, attempts atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded.Add(1) }))
	defer destination.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	c, err := New("ida", ServerConfig{URL: upstream.URL, Headers: map[string]string{"Authorization": "Bearer private"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	if err := c.Start(context.Background(), context.Background()); err == nil {
		t.Fatal("redirected initialization succeeded")
	}
	if forwarded.Load() != 0 || attempts.Load() != 1 {
		t.Fatalf("request forwarded or replayed: forwarded=%d attempts=%d", forwarded.Load(), attempts.Load())
	}
}
