package harness_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

//go:embed testdata/orderlab/*
var orderLabFiles embed.FS

type orderLab struct {
	protected                               map[string]string
	dir, audit, url, own, foreign, baseline string
	env                                     []string
}

func setupOrderLab(t *testing.T, parent string) *orderLab {
	t.Helper()
	dir := filepath.Join(parent, "project")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	entries, err := orderLabFiles.ReadDir("testdata/orderlab")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := orderLabFiles.ReadFile("testdata/orderlab/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, strings.TrimSuffix(entry.Name(), ".txt")), data)
	}
	var seed [8]byte
	if _, err = rand.Read(seed[:]); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(seed[:])
	l := &orderLab{dir: dir, audit: filepath.Join(parent, "http-audit.jsonl"), own: "order-a-" + nonce, foreign: "order-b-" + nonce}
	users := []map[string]any{}
	for _, spec := range [][3]string{{"alice", "a", "member"}, {"bob", "b", "member"}, {"charlie", "a", "member"}, {"admin-a", "a", "admin"}, {"admin-b", "b", "admin"}} {
		users = append(users, map[string]any{"ID": spec[0], "Tenant": spec[1] + "-" + nonce, "Role": spec[2], "Token": "fixture-" + spec[0] + "-" + nonce, "Active": true, "CanRead": true})
	}
	writeEvidence(t, filepath.Join(dir, "seed.json"), map[string]any{"Users": users, "Orders": []map[string]string{{"ID": l.own, "Tenant": "a-" + nonce, "Secret": "private-a-" + nonce}, {"ID": l.foreign, "Tenant": "b-" + nonce, "Secret": "private-b-" + nonce}}})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	l.url = "http://" + addr
	writeEvidence(t, filepath.Join(dir, ".dev.json"), map[string]string{"url": l.url, "addr": addr, "audit": l.audit})
	command := exec.Command("go", "env", "-json", "GOCACHE", "GOMODCACHE", "GOPATH")
	raw, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	var goenv map[string]string
	if err = json.Unmarshal(raw, &goenv); err != nil {
		t.Fatal(err)
	}
	l.env = testEnvironment(false)
	for k, v := range goenv {
		l.env = append(l.env, k+"="+v)
	}
	l.baseline = l.revision(t)
	l.protected = map[string]string{}
	for _, name := range []string{"seed.json", ".dev.json"} {
		sum := sha256.Sum256(readFile(t, filepath.Join(dir, name)))
		l.protected[name] = hex.EncodeToString(sum[:])
	}
	t.Cleanup(func() {
		if _, err := l.dev("stop"); err != nil {
			t.Errorf("stop OrderLab: %v", err)
		}
	})
	if _, err = l.dev("start"); err != nil {
		t.Fatal(err)
	}
	return l
}

func (l *orderLab) revision(t *testing.T) string {
	t.Helper()
	h := sha256.New()
	for _, name := range []string{"main.go", "policy.go"} {
		h.Write([]byte(name))
		h.Write(readFile(t, filepath.Join(l.dir, name)))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func (l *orderLab) dev(args ...string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python", append([]string{"dev.py"}, args...)...)
	cmd.Dir = l.dir
	cmd.Env = l.env
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("dev %s: %w: %s", args[0], err, redactSecrets(raw))
	}
	var value map[string]any
	if err = json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("dev output: %w: %s", err, redactSecrets(raw))
	}
	return value, nil
}
func (l *orderLab) request(t *testing.T, method, path, user, body string) map[string]any {
	t.Helper()
	args := []string{"request", method, path, user}
	if body != "" {
		args = append(args, body)
	}
	r, err := l.dev(args...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func (l *orderLab) unchanged(t *testing.T) {
	t.Helper()
	for name, want := range l.protected {
		sum := sha256.Sum256(readFile(t, filepath.Join(l.dir, name)))
		if hex.EncodeToString(sum[:]) != want {
			t.Fatalf("protected task input changed: %s", name)
		}
	}
	for _, name := range []string{"main.go", "dev.py", "policy_test.go", "go.mod", "go.sum", "README.md"} {
		source := name
		if name == "go.mod" {
			source += ".txt"
		}
		original, err := orderLabFiles.ReadFile("testdata/orderlab/" + source)
		if err != nil {
			t.Fatal(err)
		}
		if string(readFile(t, filepath.Join(l.dir, name))) != string(original) {
			t.Fatalf("protected fixture file changed: %s", name)
		}
	}
}

// Check externally observed HTTP behavior with fresh data, including positive
// controls. Never treat the candidate's own unit tests as the only verdict.
func checkOrderLab(t *testing.T, l *orderLab, fixed bool) {
	t.Helper()
	check := func(method, path, user, body string, want int) map[string]any {
		t.Helper()
		r := l.request(t, method, path, user, body)
		if r["status"] != float64(want) {
			t.Fatalf("%s %s as %s: expected %d, got %v", method, path, user, want, r)
		}
		if r["request_id"] == "" || r["revision"] != l.revision(t) {
			t.Fatalf("unattributed response: %v", r)
		}
		return r
	}
	denied := 200
	if fixed {
		denied = 403
	}
	check("GET", "/docs", "none", "", 200)
	check("GET", "/orders/"+l.own, "alice", "", 200)
	check("GET", "/orders/"+l.own, "admin-a", "", 200)
	check("GET", "/orders/"+l.foreign, "alice", "", denied)
	check("GET", "/orders/"+l.foreign, "admin-a", "", denied)
	check("GET", "/orders/"+l.own, "none", "", 401)
	check("GET", "/orders/nonexistent", "alice", "", 404)
	check("GET", "/orders/"+l.foreign+"/summary", "alice", "", 403)
	check("GET", "/admin/users", "alice", "", 403)
	check("GET", "/admin/users/bob", "admin-a", "", 403)
	check("POST", "/exports", "alice", fmt.Sprintf(`{"order_id":%q}`, l.foreign), 403)
	r := check("POST", "/exports", "alice", fmt.Sprintf(`{"order_id":%q}`, l.own), 202)
	id, _ := field(r, "body", "ID").(string)
	if id == "" {
		t.Fatal("missing export ID")
	}
	check("GET", "/exports/"+id, "alice", "", 503)
	ready := false
	for i := 0; i < 20; i++ {
		r = check("GET", "/exports/"+id, "alice", "", 200)
		if field(r, "body", "Status") == "completed" {
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("export never completed")
	}
	check("GET", "/exports/"+id+"/download", "alice", "", 200)
	check("GET", "/exports/"+id+"/download", "admin-a", "", 200)
	check("GET", "/exports/"+id+"/download", "bob", "", denied)
	check("GET", "/exports/"+id+"/download", "charlie", "", denied)
	check("GET", "/exports/"+id+"/download", "admin-b", "", denied)
	check("GET", "/exports/"+id+"/download", "none", "", 401)
	check("PATCH", "/admin/users/alice", "admin-a", `{"can_read":false}`, 200)
	check("GET", "/orders/"+l.own, "alice", "", denied)
	check("GET", "/exports/"+id+"/download", "alice", "", denied)
	check("GET", "/exports/"+id+"/download", "admin-a", "", 200)
	check("PATCH", "/admin/users/alice", "admin-a", `{"can_read":true}`, 200)
	check("GET", "/exports/"+id+"/download", "alice", "", 200)
	check("DELETE", "/exports/"+id, "alice", "", 200)
	check("GET", "/exports/"+id+"/download", "alice", "", 404)
}

const orderLabReferencePolicy = `package main
func allowOrder(u User,o Order) bool {return u.Active && u.CanRead && u.Tenant==o.Tenant}
func allowDownload(u User,e Export,o Order) bool {return allowOrder(u,o) && (e.Owner==u.ID || u.Role=="admin")}
`

func TestUserOrderLabFixtureContract(t *testing.T) {
	for _, fixed := range []bool{false, true} {
		t.Run(fmt.Sprintf("fixed=%v", fixed), func(t *testing.T) {
			dir, err := os.MkdirTemp(artifactRoot, "orderlab-contract-")
			if err != nil {
				t.Fatal(err)
			}
			l := setupOrderLab(t, dir)
			if fixed {
				writeFile(t, filepath.Join(l.dir, "policy.go"), []byte(orderLabReferencePolicy))
				if _, err = l.dev("restart"); err != nil {
					t.Fatal(err)
				}
			}
			checkOrderLab(t, l, fixed)
			cmd := exec.Command("go", "test", "-mod=mod", "./...")
			cmd.Dir = l.dir
			cmd.Env = l.env
			if raw, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("fixture unit tests: %v %s", err, raw)
			}
			t.Logf("real HTTP fixture contract passed: %s", dir)
		})
	}
}

func TestUserIOALongTaskRuntimeSmoke(t *testing.T) {
	w := newWorkspace(t)
	l := setupOrderLab(t, w.dir)
	p := startStdioClient(t, w, "long-runtime", "", "long-runtime", stdioAgentMode{commandOnly: true, longTask: true, workDir: l.dir, environment: l.env})
	out, err := p.command(t, "python dev.py status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, l.baseline) {
		t.Fatalf("Agent command cannot inspect task service: %s", out)
	}
	out, err = p.command(t, "go test -mod=mod ./...")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ok") || !strings.Contains(out, "orderlab") {
		t.Fatalf("Agent command cannot build/test workspace: %s", out)
	}
	if _, err = p.command(t, "ioa space long-runtime harness"); err != nil {
		t.Fatal(err)
	}
	first := sendIOAFromProcess(t, p, `ioa send --content '{"text":"task workspace ready"}'`)
	r := p.request(t, "aop.ProtocolMessage", "closeSessionRequest", map[string]any{"sessionId": "operator"})
	if field(r, "closeSessionResponse", "accepted") == nil {
		t.Fatal(r)
	}
	r = p.request(t, "aop.ProtocolMessage", "openSessionRequest", map[string]any{"sessionId": "operator"})
	if field(r, "openSessionResponse", "accepted") == nil {
		t.Fatal(r)
	}
	rows := readIOAMessages(t, p, "ioa read --all --limit 10")
	if len(rows) != 1 || rows[0].ID != first.ID {
		t.Fatal("fresh Session cannot read task history")
	}
	l.unchanged(t)
}

func readAudit(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := []map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err = json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	return out
}
