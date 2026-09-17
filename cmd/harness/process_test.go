package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	buildOnce      sync.Once
	buildErr       error
	executablePath string
	artifactRoot   string
)

// The harness deliberately imports no application implementation packages.
// Every scenario drives the same executable a user starts from the command line.
func TestMain(m *testing.M) {
	if pidText := os.Getenv("CYBER_HARNESS_SIGNAL_PID"); pidText != "" {
		pid, err := strconv.Atoi(pidText)
		if err == nil && pid > 0 {
			err = interruptProcess(pid)
		} else {
			err = fmt.Errorf("invalid child pid")
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(runHarness(m))
}

func runHarness(m *testing.M) int {
	root, err := repositoryRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	parent := os.Getenv("CYBER_HARNESS_ARTIFACTS")
	if parent == "" {
		parent = filepath.Join(root, ".runlogs", "harness")
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	artifactRoot, err = os.MkdirTemp(parent, time.Now().UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	artifactRoot, err = filepath.Abs(artifactRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	executablePath = filepath.Join(artifactRoot, "aiscan")
	if runtime.GOOS == "windows" {
		executablePath += ".exe"
	}
	defer os.Remove(executablePath)
	fmt.Fprintln(os.Stderr, "harness artifacts:", artifactRoot)
	return m.Run()
}

func repositoryRoot() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("repository root not found")
		}
		current = parent
	}
}

func buildExecutable(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		root, err := repositoryRoot()
		if err != nil {
			buildErr = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		// The full manifest includes CSTX, which only compiles with cgo. The
		// harness runner's default CGO_ENABLED=1 covers that.
		cmd := exec.CommandContext(ctx, "go", "build", "-tags", "full", "-o", executablePath, "./cmd/aiscan")
		cmd.Dir = root
		data, err := cmd.CombinedOutput()
		if writeErr := os.WriteFile(filepath.Join(artifactRoot, "build.log"), data, 0600); writeErr != nil {
			buildErr = writeErr
			return
		}
		if err != nil {
			buildErr = fmt.Errorf("build current full application: %w\n%s", err, data)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return executablePath
}

type workspace struct {
	dir    string
	config string
	db     string
	starts int
}

func newWorkspace(t *testing.T) *workspace {
	t.Helper()
	buildExecutable(t)
	dir, err := os.MkdirTemp(artifactRoot, t.Name()+"-")
	if err != nil {
		t.Fatal(err)
	}
	w := &workspace{dir: dir, config: filepath.Join(dir, "cyber.yaml"), db: filepath.Join(dir, "web.db")}
	writeFile(t, w.config, []byte("{}\n"))
	t.Logf("workspace: %s", dir)
	return w
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return redactSecrets(data)
}

func redactSecrets(data []byte) []byte {
	key := strings.TrimSpace(os.Getenv("CYBER_HARNESS_LLM_API_KEY"))
	if key == "" {
		return data
	}
	// Match both plaintext logs and JSON-escaped error bodies.
	encoded, _ := json.Marshal(key)
	data = bytes.ReplaceAll(data, encoded[1:len(encoded)-1], []byte("[REDACTED]"))
	return bytes.ReplaceAll(data, []byte(key), []byte("[REDACTED]"))
}

// Buffer complete log lines so a credential split across Write calls cannot
// escape redaction. Raw child output is never written to an artifact file.
type processLog struct {
	mu      sync.Mutex
	file    *os.File
	pending []byte
	closed  bool
}

// newLog opens an artifact log whose file is released when the test ends. The
// caller may still Close it earlier to flush and report the error; Close is
// idempotent so both paths can run.
func newLog(t *testing.T, path string) *processLog {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	log := &processLog{file: file}
	t.Cleanup(func() { _ = log.Close() })
	return log
}

func (l *processLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pending = append(l.pending, data...)
	for {
		end := bytes.IndexByte(l.pending, '\n')
		if end < 0 {
			break
		}
		if _, err := l.file.Write(redactSecrets(l.pending[:end+1])); err != nil {
			return 0, err
		}
		l.pending = l.pending[end+1:]
	}
	if len(l.pending) > 1<<20 {
		l.pending = nil
		if _, err := l.file.WriteString("[oversized log line omitted]\n"); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func (l *processLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	_, writeErr := l.file.Write(redactSecrets(l.pending))
	l.pending = nil
	return errors.Join(writeErr, l.file.Close())
}

type testProcess struct {
	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error
	url     string
	logPath string
}

// Use an explicit config and isolated data directory. Personal credentials and
// endpoint environment settings must not affect a reproducible local scenario.
func testEnvironment(includeLLM bool) []string {
	// Keep only operating-system and native-loader settings. In particular,
	// LLM_*, OPENAI_*, vendor credentials and IOA settings are not inherited.
	allowed := map[string]bool{
		"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "WINDIR": true,
		"COMSPEC": true, "SYSTEMDRIVE": true, "TEMP": true, "TMP": true,
		"TMPDIR": true, "LANG": true, "LC_ALL": true,
		"LD_LIBRARY_PATH": true, "DYLD_LIBRARY_PATH": true,
	}
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if allowed[strings.ToUpper(name)] {
			env = append(env, entry)
		}
	}
	if includeLLM {
		for source, target := range map[string]string{
			"CYBER_HARNESS_LLM_API_KEY":  "CYBER_API_KEY",
			"CYBER_HARNESS_LLM_BASE_URL": "CYBER_BASE_URL",
			"CYBER_HARNESS_LLM_MODEL":    "CYBER_MODEL",
			"CYBER_HARNESS_LLM_PROVIDER": "CYBER_PROVIDER",
		} {
			if value := os.Getenv(source); value != "" {
				env = append(env, target+"="+value)
			}
		}
	}
	return env
}

func (w *workspace) launch(t *testing.T, includeLLM bool) *testProcess {
	t.Helper()
	w.starts++
	p := &testProcess{done: make(chan struct{}), logPath: filepath.Join(w.dir, fmt.Sprintf("process-%02d.log", w.starts))}
	output := newLog(t, p.logPath)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	p.cmd = exec.CommandContext(ctx, executablePath,
		"--config", w.config, "--data-dir", filepath.Join(w.dir, "data"), "--no-color",
		"web", "--addr", "127.0.0.1:0",
		"--db", w.db, "--token", "harness-local-access", "--no-agent")
	p.cmd.Dir = w.dir
	p.cmd.Env = testEnvironment(includeLLM)
	p.cmd.Stdout, p.cmd.Stderr = output, output
	configureProcess(p.cmd)
	if err := p.cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	go func() {
		p.waitErr = p.cmd.Wait()
		p.waitErr = errors.Join(p.waitErr, output.Close())
		close(p.done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
			t.Errorf("child process did not exit: %s", p.logPath)
		}
	})
	return p
}

var listening = regexp.MustCompile(`aiscan server listening on http://(127\.0\.0\.1:\d+)`)

func (w *workspace) start(t *testing.T) *testProcess {
	t.Helper()
	return w.startMode(t, false)
}

func (w *workspace) startMode(t *testing.T, includeLLM bool) *testProcess {
	t.Helper()
	p := w.launch(t, includeLLM)
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	client := &http.Client{Timeout: time.Second}
	for {
		data, err := os.ReadFile(p.logPath)
		if err != nil {
			t.Fatal(err)
		}
		if match := listening.FindSubmatch(data); len(match) == 2 {
			p.url = "http://" + string(match[1])
			resp, err := client.Get(p.url + "/health")
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					t.Logf("application ready pid=%d url=%s", p.cmd.Process.Pid, p.url)
					return p
				}
			}
		}
		select {
		case <-p.done:
			t.Fatalf("application exited before ready: %v\n%s", p.waitErr, readFile(t, p.logPath))
		case <-deadline.C:
			t.Fatalf("application readiness timed out\n%s", readFile(t, p.logPath))
		case <-ticker.C:
		}
	}
}

func (p *testProcess) crash(t *testing.T) {
	t.Helper()
	select {
	case <-p.done:
		t.Fatalf("application exited before crash scenario: %v", p.waitErr)
	default:
	}
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Fatal("killed process did not exit")
	}
}

func (p *testProcess) interrupt(t *testing.T) {
	t.Helper()
	// A separate controller preserves the test runner's console on Windows.
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	controller := exec.CommandContext(ctx, bin, "-test.run=^$")
	controller.Env = append(testEnvironment(false), "CYBER_HARNESS_SIGNAL_PID="+strconv.Itoa(p.cmd.Process.Pid))
	if output, err := controller.CombinedOutput(); err != nil {
		t.Fatalf("interrupt application: %v\n%s", err, output)
	}
}

type userClient struct {
	client *http.Client
	base   string
	trace  *os.File
	mu     sync.Mutex
}

func (p *testProcess) user(t *testing.T, name string) *userClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	trace, err := os.Create(filepath.Join(filepath.Dir(p.logPath), fmt.Sprintf("%s-%s.jsonl", filepath.Base(p.logPath), name)))
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{}
	u := &userClient{client: &http.Client{Jar: jar, Transport: transport, Timeout: 45 * time.Second}, base: p.url, trace: trace}
	t.Cleanup(func() { transport.CloseIdleConnections(); trace.Close() })
	u.call(t, http.MethodPost, "/api/auth/login", map[string]any{"token": "harness-local-access"}, http.StatusOK)
	return u
}

func (u *userClient) request(method, path string, input any) (map[string]any, int, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequest(method, u.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	start := time.Now()
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	data = redactSecrets(data)
	u.mu.Lock()
	traceErr := json.NewEncoder(u.trace).Encode(map[string]any{
		"time": start.UTC(), "method": method, "path": path,
		"status": resp.StatusCode, "elapsed_ms": time.Since(start).Milliseconds(), "response": string(data),
	})
	u.mu.Unlock()
	if traceErr != nil {
		return nil, resp.StatusCode, traceErr
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("%s: invalid JSON: %w: %s", path, err, data)
	}
	return result, resp.StatusCode, nil
}

func (u *userClient) call(t *testing.T, method, path string, input any, wantStatus int) map[string]any {
	t.Helper()
	result, status, err := u.request(method, path, input)
	if err != nil || status != wantStatus {
		t.Fatalf("%s %s: status=%d want=%d err=%v response=%v", method, path, status, wantStatus, err, result)
	}
	return result
}

const configRPC = "/cyber.rpc.config.ConfigService/"

func (u *userClient) config(t *testing.T) map[string]any {
	t.Helper()
	return u.call(t, http.MethodPost, configRPC+"GetConfig", map[string]any{}, http.StatusOK)
}

func field(object map[string]any, path ...string) any {
	var value any = object
	for _, name := range path {
		next, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = next[name]
	}
	return value
}

func assertField(t *testing.T, object map[string]any, want any, path ...string) {
	t.Helper()
	if got := field(object, path...); got != want {
		t.Fatalf("%s = %v, want %v; response=%v", strings.Join(path, "."), got, want, object)
	}
}
