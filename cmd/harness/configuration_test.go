package harness_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func profileConfig(active string) map[string]any {
	var profiles []any
	for _, id := range []string{"daily", "review", "offline"} {
		profiles = append(profiles, map[string]any{
			"id": id, "name": "工作配置 " + id, "provider": "openai",
			"model": "model-" + id, "baseUrl": "http://127.0.0.1:1/v1",
		})
	}
	// Saving incomplete provider settings is a real supported user workflow.
	// No API key is configured, so the product makes no model calls.
	return map[string]any{"llm": map[string]any{"activeProfile": active, "providers": profiles}}
}

func assertProfile(t *testing.T, response map[string]any, id string) {
	t.Helper()
	assertField(t, response, id, "config", "llm", "activeProfile")
	assertField(t, response, id, "config", "llm", "active", "id")
	assertField(t, response, "model-"+id, "config", "llm", "active", "model")
}

func TestUserConfigurationAcrossCrashAndRestart(t *testing.T) {
	w := newWorkspace(t)
	p := w.start(t)
	editor, observer := p.user(t, "editor"), p.user(t, "observer")
	assertField(t, editor.config(t), true, "config", "loaded")
	status := editor.call(t, http.MethodPost, "/cyber.rpc.system.SystemService/GetStatus", map[string]any{}, http.StatusOK)
	if available, _ := field(status, "status", "llmAvailable").(bool); available {
		t.Fatal("no-LLM workspace unexpectedly inherited a model provider")
	}
	missingKey := editor.call(t, http.MethodPost, configRPC+"TestLLM", map[string]any{
		"provider": "openai", "model": "unconfigured", "baseUrl": "http://127.0.0.1:1/v1",
	}, http.StatusOK)
	if ok, _ := field(missingKey, "ok").(bool); ok || field(missingKey, "error") == nil || field(missingKey, "error") == "" {
		t.Fatalf("missing LLM configuration was not reported to the user: %v", missingKey)
	}

	t.Log("save profiles in one client and observe them from another")
	incoming := profileConfig("daily")
	incoming["search"] = map[string]any{"tavilyKeys": "harness-only-placeholder"}
	response := editor.call(t, http.MethodPost, configRPC+"UpdateConfig", map[string]any{"config": incoming}, http.StatusOK)
	assertProfile(t, response, "daily")
	assertProfile(t, observer.config(t), "daily")
	if !bytes.Contains(readFile(t, w.config), []byte("model-daily")) {
		t.Fatal("successful save did not reach the config file")
	}

	t.Log("invalid edit must preserve the saved file and the other client's view")
	committed := readFile(t, w.config)
	bad := profileConfig("daily")
	bad["llm"].(map[string]any)["providers"].([]any)[0].(map[string]any)["maxTokens"] = -1
	editor.call(t, http.MethodPost, configRPC+"UpdateConfig", map[string]any{"config": bad}, http.StatusBadRequest)
	if !bytes.Equal(committed, readFile(t, w.config)) {
		t.Fatal("rejected edit changed the committed file")
	}
	assertProfile(t, observer.config(t), "daily")
	editor.call(t, http.MethodPost, configRPC+"ActivateProfile", map[string]any{"profileId": "does-not-exist"}, http.StatusNotFound)
	if !bytes.Equal(committed, readFile(t, w.config)) {
		t.Fatal("rejected activation changed the committed file")
	}

	t.Log("recover with a valid edit; blank secret fields retain the existing setting")
	next := profileConfig("review")
	next["search"] = map[string]any{"tavilyKeys": ""}
	editor.call(t, http.MethodPost, configRPC+"UpdateConfig", map[string]any{"config": next}, http.StatusOK)
	assertProfile(t, observer.config(t), "review")
	assertField(t, observer.config(t), true, "config", "search", "tavilyKeysConfigured")
	if !bytes.Contains(readFile(t, w.config), []byte("harness-only-placeholder")) {
		t.Fatal("blank secret edit lost the stored setting")
	}
	temps, err := filepath.Glob(filepath.Join(w.dir, ".cyber.yaml.tmp-*.yaml"))
	if err != nil || len(temps) != 0 {
		t.Fatalf("config staging files remain after requests: %v, %v", temps, err)
	}

	t.Log("terminate the process abruptly and reopen the same workspace")
	p.crash(t)
	p = w.start(t)
	reopened := p.user(t, "reopened")
	assertProfile(t, reopened.config(t), "review")
	assertField(t, reopened.config(t), true, "config", "search", "tavilyKeysConfigured")
	reopened.call(t, http.MethodPost, configRPC+"ActivateProfile", map[string]any{"profileId": "offline"}, http.StatusOK)
	assertProfile(t, reopened.config(t), "offline")

	t.Log("logout and login again using the browser cookie workflow")
	reopened.call(t, http.MethodPost, "/api/auth/logout", map[string]any{}, http.StatusOK)
	assertField(t, reopened.call(t, http.MethodGet, "/api/auth/session", nil, http.StatusOK), false, "authenticated")
	reopened.call(t, http.MethodPost, "/api/auth/login", map[string]any{"token": "harness-local-access"}, http.StatusOK)
	assertProfile(t, reopened.config(t), "offline")
}

func TestUserConcurrentProfileChanges(t *testing.T) {
	w := newWorkspace(t)
	p := w.start(t)
	writer := p.user(t, "writer")
	writer.call(t, http.MethodPost, configRPC+"UpdateConfig", map[string]any{"config": profileConfig("daily")}, http.StatusOK)
	seed := time.Now().UnixNano()
	if supplied := os.Getenv("CYBER_HARNESS_SEED"); supplied != "" {
		parsed, err := strconv.ParseInt(supplied, 10, 64)
		if err != nil {
			t.Fatalf("invalid CYBER_HARNESS_SEED: %v", err)
		}
		seed = parsed
	}
	writeFile(t, filepath.Join(w.dir, "seed.txt"), []byte(strconv.FormatInt(seed, 10)+"\n"))
	t.Logf("replay with CYBER_HARNESS_SEED=%d", seed)
	random := rand.New(rand.NewSource(seed))
	steps := 12
	if configured := os.Getenv("CYBER_HARNESS_STEPS"); configured != "" {
		parsed, err := strconv.Atoi(configured)
		if err != nil || parsed < 1 || parsed > 100 {
			t.Fatal("CYBER_HARNESS_STEPS must be an integer from 1 to 100")
		}
		steps = parsed
	}
	ids := []string{"daily", "review", "offline"}
	readers := []*userClient{p.user(t, "tab-a"), p.user(t, "tab-b"), p.user(t, "tab-c")}
	last := "daily"
	for step := 0; step < steps; step++ {
		selected := ids[random.Intn(len(ids))]
		start := make(chan struct{})
		failures := make(chan error, len(readers))
		var running sync.WaitGroup
		for _, reader := range readers {
			running.Add(1)
			go func(reader *userClient, previous string) {
				defer running.Done()
				<-start
				for sample := 0; sample < 3; sample++ {
					response, status, err := reader.request(http.MethodPost, configRPC+"GetConfig", map[string]any{})
					if err != nil || status != http.StatusOK {
						failures <- fmt.Errorf("read during save: status=%d err=%v", status, err)
						return
					}
					id, _ := field(response, "config", "llm", "activeProfile").(string)
					if (id != previous && id != selected) || field(response, "config", "llm", "active", "id") != id || field(response, "config", "llm", "active", "model") != "model-"+id {
						failures <- fmt.Errorf("inconsistent profile snapshot: %v", response)
						return
					}
				}
			}(reader, last)
		}
		close(start)
		// Wait for readers even if the write fails, so no work outlives its test.
		response, status, err := writer.request(http.MethodPost, configRPC+"ActivateProfile", map[string]any{"profileId": selected})
		running.Wait()
		close(failures)
		for failure := range failures {
			t.Error(failure)
		}
		if err != nil || status != http.StatusOK {
			t.Fatalf("step %d write: status=%d err=%v response=%v", step, status, err, response)
		}
		assertProfile(t, response, selected)
		for _, reader := range readers {
			assertProfile(t, reader.config(t), selected)
		}
		last = selected
		t.Logf("step %02d: committed %s and checked all clients", step, last)
	}
	p.crash(t)
	p = w.start(t)
	assertProfile(t, p.user(t, "after-restart").config(t), last)
}

func TestUserStartupRecoveryAndConfirmedExit(t *testing.T) {
	w := newWorkspace(t)
	writeFile(t, w.config, []byte("llm: [unterminated\n"))
	broken := w.launch(t, false)
	select {
	case <-broken.done:
		if broken.waitErr == nil {
			t.Fatal("invalid config unexpectedly produced a successful exit")
		}
		if !strings.Contains(strings.ToLower(string(readFile(t, broken.logPath))), "config") {
			t.Fatal("startup failure did not explain the config error")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("invalid config did not fail promptly")
	}
	t.Log("correct the file and start the same workspace")
	writeFile(t, w.config, []byte("{}\n"))
	p := w.start(t)
	u := p.user(t, "user")
	assertField(t, u.config(t), true, "config", "loaded")
	u.call(t, http.MethodPost, configRPC+"UpdateConfig", map[string]any{"config": profileConfig("daily")}, http.StatusOK)
	t.Log("first exit signal asks for confirmation while the service remains usable")
	p.interrupt(t)
	confirmation := time.NewTimer(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer confirmation.Stop()
	defer ticker.Stop()
	for !bytes.Contains(readFile(t, p.logPath), []byte("Press Ctrl+C again to exit")) {
		select {
		case <-p.done:
			t.Fatalf("product exited before confirmation: %v", p.waitErr)
		case <-confirmation.C:
			t.Fatal("product did not show its exit confirmation")
		case <-ticker.C:
		}
	}
	assertProfile(t, u.config(t), "daily")
	t.Log("confirm exit, then verify resources and persisted configuration")
	p.interrupt(t)
	select {
	case <-p.done:
		// The current CLI deliberately exits with 130 after confirmation. This
		// checks the public behavior, not graceful application resource cleanup.
		if p.cmd.ProcessState.ExitCode() != 130 {
			t.Fatalf("confirmed exit: got %v, want code 130\n%s", p.waitErr, readFile(t, p.logPath))
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("product did not exit after the user's signal\n%s", readFile(t, p.logPath))
	}
	listener, err := net.Listen("tcp", strings.TrimPrefix(p.url, "http://"))
	if err != nil {
		t.Fatalf("product did not release its listener: %v", err)
	}
	listener.Close()
	if info, err := os.Stat(w.db); err != nil || info.Size() == 0 {
		t.Fatalf("product did not create its database: %v", err)
	}
	// Verify the operating system has released the product's database handle.
	if err := os.Rename(w.db, w.db+".closed"); err != nil {
		t.Fatalf("database still held after shutdown: %v", err)
	}
	if err := os.Rename(w.db+".closed", w.db); err != nil {
		t.Fatal(err)
	}
	p = w.start(t)
	assertProfile(t, p.user(t, "restarted").config(t), "daily")
}
