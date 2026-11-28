package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	coretool "github.com/chainreactors/cyber/core/tool"
)

const testKey = "cr_sub_test"

type recorded struct {
	method, path, auth string
	query              map[string][]string
	body               map[string]any
}

func fakeRelay(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) (*Client, func() []recorded) {
	t.Helper()
	var mu sync.Mutex
	var calls []recorded
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if data, _ := io.ReadAll(r.Body); len(data) > 0 {
			if err := json.Unmarshal(data, &body); err != nil {
				t.Errorf("%s %s: body is not JSON: %s", r.Method, r.URL.Path, data)
			}
		}
		mu.Lock()
		calls = append(calls, recorded{r.Method, r.URL.EscapedPath(), r.Header.Get("Authorization"), r.URL.Query(), body})
		mu.Unlock()
		if answer != nil {
			answer(w, r)
			return
		}
		fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL + "/tenant/", Key: " " + testKey + " "})
	if err != nil {
		t.Fatal(err)
	}
	return client, func() []recorded {
		mu.Lock()
		defer mu.Unlock()
		return append([]recorded(nil), calls...)
	}
}

// Every generated operation reaches its documented method and path, and only
// the probes go without the key.
func TestEveryOperationCallsItsRoute(t *testing.T) {
	client, calls := fakeRelay(t, nil)
	if len(operations) != 22 {
		t.Fatalf("expected the 22 data-plane operations outside /v1/keys, got %d", len(operations))
	}
	for _, op := range operations {
		var path []string
		want := op.Path
		for _, f := range op.Fields {
			if f.In == "path" {
				path = append(path, "a/b")
				want = strings.Replace(want, "{"+f.Name+"}", "a%2Fb", 1)
			}
		}
		var body, params any
		if op.Body != nil {
			body = op.Body()
		}
		if op.Params != nil {
			params = op.Params()
		}
		if _, err := op.Call(t.Context(), client, path, body, params); err != nil {
			t.Fatalf("%s: %v", op.Name, err)
		}
		got := calls()[len(calls())-1]
		if got.method != op.Method || got.path != "/tenant"+want {
			t.Errorf("%s: sent %s %s, want %s /tenant%s", op.Name, got.method, got.path, op.Method, want)
		}
		probe := op.Name == "healthz" || op.Name == "readyz"
		if probe != (got.auth == "") || (!probe && got.auth != "Bearer "+testKey) {
			t.Errorf("%s: Authorization %q", op.Name, got.auth)
		}
	}
}

func TestQueryParameters(t *testing.T) {
	client, calls := fakeRelay(t, nil)
	if _, err := client.LookupHost(t.Context(), "1.2.3.4", LookupHostParams{RouteMode: "fanout", Format: "flat"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListSMSProjects(t.Context(), ListSMSProjectsParams{Search: "example", PageSize: 20}); err != nil {
		t.Fatal(err)
	}
	host, sms := calls()[0], calls()[1]
	if host.query["route_mode"][0] != "fanout" || host.query["format"][0] != "flat" || host.query["group"] != nil {
		t.Errorf("host query: %v", host.query)
	}
	if sms.query["search"][0] != "example" || sms.query["page_size"][0] != "20" || sms.query["page"] != nil {
		t.Errorf("sms query: %v", sms.query)
	}
}

func TestErrors(t *testing.T) {
	client, calls := fakeRelay(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tenant/v1/tools/sms/leases/l1/code":
			w.Header().Set("X-Relay-Request-ID", "req-1")
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"code":"SMS_POLL_TOO_FAST","message":"too fast","hint":"wait 5 seconds"}}`)
		case "/tenant/v1/web/fetch":
			http.Redirect(w, r, "https://elsewhere.example/", http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "404 page not found")
		}
	})
	resp, err := client.PollSMSCode(t.Context(), "l1")
	var relayErr *Error
	if resp != nil || !errors.As(err, &relayErr) || relayErr.Code != "SMS_POLL_TOO_FAST" || relayErr.Hint != "wait 5 seconds" || relayErr.RetryAfter != 5*time.Second || relayErr.RequestID != "req-1" {
		t.Fatalf("relay error: resp=%#v err=%#v", resp, err)
	}
	for _, want := range []string{"429", "SMS_POLL_TOO_FAST", "hint: wait 5 seconds", "retry after 5s", "req-1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error text %q lacks %q", err, want)
		}
	}
	if _, err := client.FetchWebPage(t.Context(), WebFetchRequest{URL: "https://a.test"}); !errors.As(err, &relayErr) || relayErr.Status != http.StatusFound {
		t.Fatalf("a redirect must not be followed: %v", err)
	}
	if _, err := client.Healthz(t.Context()); !errors.As(err, &relayErr) || relayErr.Status != 404 || relayErr.Message != "404 page not found" {
		t.Fatalf("plain-text error: %v", err)
	}
	// Path traversal or empty path parameters must fail before sending a request.
	before := len(calls())
	for _, bad := range []string{"", ".", ".."} {
		if resp, err := client.LookupHost(t.Context(), bad, LookupHostParams{}); err == nil || resp != nil {
			t.Errorf("LookupHost(%q) = (%#v, %v), want error", bad, resp, err)
		}
		if resp, err := client.PollSMSCode(t.Context(), bad); err == nil || resp != nil {
			t.Errorf("PollSMSCode(%q) = (%#v, %v), want error", bad, resp, err)
		}
	}
	if len(calls()) != before {
		t.Fatalf("invalid path parameters sent %d HTTP requests", len(calls())-before)
	}
}

func TestNewRejectsUnusableConfig(t *testing.T) {
	for _, config := range []Config{
		{BaseURL: "ftp://relay.test", Key: "k"},
		{BaseURL: "https://user:pass@relay.test", Key: "k"},
		{BaseURL: "https://relay.test?x=1", Key: "k"},
		{BaseURL: "https://relay.test?", Key: "k"},
		{BaseURL: "https://relay.test", Key: "  "},
	} {
		if _, err := New(config); err == nil {
			t.Errorf("accepted %+v", config)
		}
	}
}

func runCommand(t *testing.T, client *Client, dir string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	_, err := NewCommand(client).Run(t.Context(), &coretool.Execution{Args: args, Dir: dir, Stdout: &out})
	return out.String(), err
}

func TestCommandFlags(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "c.png"), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "body.json"), []byte(`{"region":"global","country":"US"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	client, calls := fakeRelay(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tenant/v1/proxy/acquire" {
			fmt.Fprint(w, `{"region":"global","refreshed":false,"proxies":[{"lease_id":"5f0e8c1a-0000-4000-8000-000000000001","session_id":"task01","region":"global","protocol":"socks5","tunnel_url":"socks5://cr_sub_test:global.us.sticky.task01@relay:18081"}]}`)
			return
		}
		fmt.Fprint(w, `{}`)
	})
	cases := []struct {
		args  []string
		check func(recorded) bool
	}{
		{[]string{"search-assets", `domain="a.test"`, "&&", "port=443", "--size", "20", "--route-mode", "fanout", "--cache=false", "--strict"}, func(r recorded) bool {
			return r.body["query"] == `domain="a.test" && port=443` && r.body["size"] == float64(20) &&
				r.body["route_mode"] == "fanout" && r.body["cache"] == false && r.body["strict"] == true
		}},
		{[]string{"solve-captcha", "image_text", "--image", "@c.png", "--options", "a=1", "--options=b=2"}, func(r recorded) bool {
			return r.body["type"] == "image_text" && r.body["image"] == "cG5n" && fmt.Sprint(r.body["options"]) == "map[a:1 b:2]"
		}},
		{[]string{"acquire-proxy", "--body", "@body.json", "--session-id", "task01"}, func(r recorded) bool {
			return r.body["region"] == "global" && r.body["country"] == "US" && r.body["session_id"] == "task01"
		}},
		{[]string{"poll-sms-code", "lease-1"}, func(r recorded) bool { return r.path == "/tenant/v1/tools/sms/leases/lease-1/code" }},
		{[]string{"lookup-host", "1.2.3.4", "--format", "flat"}, func(r recorded) bool { return r.query["format"][0] == "flat" }},
		{[]string{"remove-proxy-whitelist", "1.2.3.4", "5.6.7.0/24"}, func(r recorded) bool { return fmt.Sprint(r.body["ips"]) == "[1.2.3.4 5.6.7.0/24]" }},
		{[]string{"remove-proxy-whitelist", "--body", `{"ips":["1.1.1.1"]}`, "--ips", "2.2.2.2"}, func(r recorded) bool { return fmt.Sprint(r.body["ips"]) == "[2.2.2.2]" }},
		{[]string{"search-web", "q", "--body", `{"options":{"a":"1","b":"2"}}`, "--options", "c=3"}, func(r recorded) bool { return fmt.Sprint(r.body["options"]) == "map[c:3]" }},
		{[]string{"search-web", "--", "docker", "--network", "host", "-h"}, func(r recorded) bool { return r.body["query"] == "docker --network host -h" }},
		{[]string{"list-sms-projects", "--search", "-h"}, func(r recorded) bool { return r.query["search"][0] == "-h" }},
	}
	for _, c := range cases {
		out, err := runCommand(t, client, dir, c.args...)
		if err != nil {
			t.Fatalf("relay %v: %v", c.args, err)
		}
		if got := calls()[len(calls())-1]; !c.check(got) {
			t.Errorf("relay %v sent %+v", c.args, got)
		}
		// The tunnel URL carries the relay key and is shown as is: the agent
		// uses it as a proxy URL.
		if c.args[0] == "acquire-proxy" && !strings.Contains(out, "socks5://cr_sub_test:global.us.sticky.task01@relay:18081") {
			t.Errorf("relay %v output: %s", c.args, out)
		}
	}
	for _, args := range [][]string{
		{"nope"},
		{"search-assets"},
		{"search-assets", "x", "--bogus", "1"},
		{"search-assets", "x", "--size"},
		{"search-assets", "x", "--size", "many"},
		{"poll-sms-code"},
		{"proxy-status", "extra"},
		{"acquire-proxy", "--body", `{"unknown":1}`},
		{"acquire-proxy", "--body", `{"region":"global"}{"country":"US"}`},
		{"acquire-proxy", "--body", `{"region":"global"}}`},
		{"acquire-proxy", "--body", ""},
		{"search-web", "--query", "--cache=false"},
	} {
		if _, err := runCommand(t, client, dir, args...); err == nil {
			t.Errorf("relay %v accepted", args)
		}
	}
	big := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(big, make([]byte, maxRequest+1), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"acquire-proxy", "--body", "@" + big},
		{"solve-captcha", "image_text", "--image", "@" + big},
	} {
		if _, err := runCommand(t, client, dir, args...); err == nil {
			t.Errorf("relay %v accepted oversized file", args)
		}
	}
}

func TestCommandHelpComesFromTheSpec(t *testing.T) {
	client, calls := fakeRelay(t, nil)
	overview, err := runCommand(t, client, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range operations {
		if !strings.Contains(overview, op.Name) {
			t.Errorf("overview lacks %s", op.Name)
		}
	}
	help, err := runCommand(t, client, "", "solve-captcha", "-h")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"POST /v1/tools/captcha", "--site-key", "--background-image", "(string, required)", "@path"} {
		if !strings.Contains(help, want) {
			t.Errorf("solve-captcha help lacks %q:\n%s", want, help)
		}
	}
	if len(calls()) != 0 {
		t.Fatal("help called the relay")
	}
}
