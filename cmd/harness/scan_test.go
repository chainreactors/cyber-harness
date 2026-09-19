package harness_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const scanRPC = "/cyber.rpc.scan.ScanService/"

func TestUserHubScanRequiresConnectedNode(t *testing.T) {
	w := newWorkspace(t)
	hub := w.start(t) // Real Web process with --no-agent, no model configured.
	u := hub.user(t, "scan-user")
	var requests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><head><title>Harness scan target</title></head><body>Local scan fixture</body></html>")
	}))
	t.Cleanup(target.Close)

	submit := func(id string) map[string]any {
		return u.call(t, http.MethodPost, scanRPC+"SubmitScan", map[string]any{
			"requestId": id, "target": target.URL, "mode": "quick",
		}, http.StatusOK)
	}
	assertRejected := func(id string) {
		t.Helper()
		response := submit(id)
		assertField(t, response, id, "requestId")
		assertField(t, response, "FAILED_PRECONDITION", "rejected", "code")
		message, _ := field(response, "rejected", "message").(string)
		if !strings.Contains(message, "--no-agent") {
			t.Fatalf("missing recovery instructions: %v", response)
		}
	}
	listScans := func() []any {
		response := u.call(t, http.MethodPost, scanRPC+"ListScans", map[string]any{}, http.StatusOK)
		scans, _ := response["scans"].([]any)
		return scans
	}
	assertRejected("before-node")
	if scans := listScans(); len(scans) != 0 {
		t.Fatalf("rejected scan created a record: %v", scans)
	}

	node := startScanNode(t, w, hub.url)
	waitNodeCount(t, u, 1)
	response := submit("with-node")
	scanID, _ := field(response, "accepted", "id").(string)
	if scanID == "" {
		t.Fatalf("scan was not accepted after node joined: %v", response)
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		response = u.call(t, http.MethodPost, scanRPC+"GetScan", map[string]any{"scanId": scanID}, http.StatusOK)
		status := field(response, "scan", "status")
		if status == "SCAN_STATUS_COMPLETED" {
			break
		}
		if status == "SCAN_STATUS_FAILED" || status == "SCAN_STATUS_CANCELED" || time.Now().After(deadline) {
			t.Fatalf("remote scan did not complete: %v\nnode log:\n%s", response, readFile(t, node.logPath))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if requests.Load() == 0 {
		t.Fatal("scan completed without contacting the HTTP target")
	}
	t.Logf("remote scan %s completed; target received %d requests", scanID, requests.Load())

	node.crash(t)
	waitNodeCount(t, u, 0)
	assertRejected("after-disconnect")
	scans := listScans()
	if len(scans) != 1 {
		t.Fatalf("want only the completed scan, got %v", scans)
	}
	response = u.call(t, http.MethodPost, scanRPC+"GetScan", map[string]any{"scanId": scanID}, http.StatusOK)
	assertField(t, response, "SCAN_STATUS_COMPLETED", "scan", "status")
}

func startScanNode(t *testing.T, w *workspace, hubURL string) *testProcess {
	t.Helper()
	p := &testProcess{done: make(chan struct{}), logPath: filepath.Join(w.dir, "scan-node.log")}
	output := newLog(t, p.logPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	serverURL := strings.Replace(hubURL, "http://", "http://harness-local-access@", 1)
	p.cmd = exec.CommandContext(ctx, executablePath,
		"--config", w.config, "--data-dir", filepath.Join(w.dir, "node-data"), "--no-color",
		"agent", "--server-url", serverURL, "--node-name", "scan-node")
	p.cmd.Dir = w.dir
	p.cmd.Env = testEnvironment(false)
	p.cmd.Stdout, p.cmd.Stderr = output, output
	configureProcess(p.cmd)
	if err := p.cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	go func() {
		p.waitErr = p.cmd.Wait()
		_ = output.Close()
		close(p.done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
			t.Errorf("scan node did not exit: %s", p.logPath)
		}
		if t.Failed() {
			t.Logf("scan node log:\n%s", readFile(t, p.logPath))
		}
	})
	return p
}

func waitNodeCount(t *testing.T, u *userClient, want int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		response := u.call(t, http.MethodPost, "/cyber.rpc.agent.AgentService/ListAgents", map[string]any{}, http.StatusOK)
		nodes, _ := response["agents"].([]any)
		if len(nodes) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("want %d nodes: %v", want, response)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
