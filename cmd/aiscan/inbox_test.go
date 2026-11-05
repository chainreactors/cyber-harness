package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/telemetry"
	cfg "github.com/chainreactors/cyber/pkg/config"
	profilepkg "github.com/chainreactors/cyber/pkg/profile"
)

func TestParseCLIInboxHost(t *testing.T) {
	parsed, err := parseCLI([]string{"inbox", "--state-dir", "state", "--max-active", "2", "--timeout", "19", "--ioa-url", "http://localhost:8765", "--space", "team"})
	if err != nil {
		t.Fatal(err)
	}
	o := parsed.Option
	if parsed.Mode != cfg.RunModeInbox || o.Command != cfg.RunModeInbox || o.StateDir != "state" || o.MaxActive != 2 || o.Timeout != 19 {
		t.Fatalf("inbox flags = %+v, command=%s", o.InboxOptions, o.Command)
	}
	if ioa := readClientOptions(t, &o); ioa.URL != "http://localhost:8765" || ioa.Space != "team" {
		t.Fatalf("inbox IOA options = %+v", ioa)
	}
}

func TestInboxCommandOwnsRouterFlagsAndHelp(t *testing.T) {
	for _, args := range [][]string{
		{"agent", "--mode", "active"},
		{"agent", "--state-dir", "state"},
		{"agent", "--max-active", "2"},
		{"inbox", "--mode", "active"},
	} {
		if _, err := parseCLIWithOutput(args, io.Discard); err == nil {
			t.Fatalf("unexpected flags accepted: %v", args)
		}
	}
	var help bytes.Buffer
	if err := runCLI(t.Context(), []string{"inbox", "--help"}, strings.NewReader(""), &help, io.Discard, nil); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"inbox [OPTIONS]", "--state-dir", "--max-active", "--ioa-url", "--model", "--prompt"} {
		if !containsAny(help.String(), flag, strings.Replace(flag, "--", "/", 1)) {
			t.Fatalf("inbox help missing %q: %s", flag, &help)
		}
	}
	help.Reset()
	if err := runCLI(t.Context(), []string{"agent", "--help"}, strings.NewReader(""), &help, io.Discard, nil); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--state-dir", "/state-dir", "--max-active", "/max-active", "--mode=", "/mode:"} {
		if strings.Contains(help.String(), flag) {
			t.Fatalf("inbox option leaked into agent: %s", flag)
		}
	}
}

func TestInboxCommandTimeoutDefaults(t *testing.T) {
	for _, test := range []struct {
		args    []string
		timeout int
	}{
		{[]string{"inbox"}, 0},
		{[]string{"agent"}, 3600},
		{[]string{"--timeout=11", "inbox"}, 11},
		{[]string{"inbox", "--timeout=19"}, 19},
		{[]string{"inbox", "--timeout=0"}, 0},
	} {
		parsed, err := parseCLI(test.args)
		if err != nil || parsed.Option.Timeout != test.timeout {
			t.Fatalf("args=%v timeout=%d error=%v", test.args, parsed.Option.Timeout, err)
		}
	}
}

func TestProfileRouterOnlyInInboxCommand(t *testing.T) {
	for _, command := range []cfg.RunMode{cfg.RunModeNoCommand, cfg.RunModeAgent, cfg.RunModeInbox} {
		t.Run("command="+string(command), func(t *testing.T) {
			config := minimalConfig(&agentsession.Config{})
			config.Option.Command, config.Option.StateDir = command, t.TempDir()
			p, err := buildAIScanProfile(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = p.Close(context.Background()) })
			if err := p.Load(t.Context()); err != nil {
				t.Fatal(err)
			}
			if (p.InboxRouter() != nil) != (command == cfg.RunModeInbox) {
				t.Fatalf("router=%v command=%q", p.InboxRouter(), command)
			}
		})
	}
}

func TestInboxHostInputLifecycle(t *testing.T) {
	for _, scenario := range []string{"EOF", "exit", "blocked reader"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			var input io.Reader = strings.NewReader("")
			var pipe *io.PipeReader
			switch scenario {
			case "exit":
				input = strings.NewReader("/exit\n")
			case "blocked reader":
				var writer *io.PipeWriter
				pipe, writer = io.Pipe()
				input = pipe
				t.Cleanup(func() { _ = writer.Close(); _ = pipe.Close() })
			}
			output, writer := io.Pipe()
			t.Cleanup(func() { _ = output.Close(); _ = writer.Close() })
			option := &cfg.Option{Command: cfg.RunModeInbox, InboxOptions: cfg.InboxOptions{StateDir: t.TempDir()}}
			var product *aiscanProfile
			factory := func(request profilepkg.Request) (profilepkg.Profile, error) {
				config := minimalConfig(request.Session)
				config.Option = request.Option
				var err error
				product, err = buildAIScanProfile(config)
				return product, err
			}
			done := make(chan error, 1)
			go func() { done <- runInbox(ctx, factory, option, telemetry.NopLogger(), input, writer) }()
			ready := make(chan error, 1)
			go func() { _, err := bufio.NewReader(output).ReadString('\n'); ready <- err }()
			select {
			case err := <-ready:
				if err != nil {
					t.Fatal(err)
				}
			case err := <-done:
				t.Fatalf("host exited before readiness: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if scenario != "exit" {
				select {
				case err := <-done:
					t.Fatalf("input detachment stopped host: %v", err)
				case <-time.After(50 * time.Millisecond):
				}
				cancel()
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("host did not drain after shutdown")
			}
			if product.Active() {
				t.Fatal("host returned without closing the profile")
			}
			if pipe != nil {
				if _, err := pipe.Read(make([]byte, 1)); err != io.ErrClosedPipe {
					t.Fatalf("input was not closed: %v", err)
				}
			}
		})
	}
}

func TestInboxHostRejectsRemoteTransport(t *testing.T) {
	for _, flags := range [][]string{{"--transport", "stdio"}, {"--transport", "web", "--server-url", "http://localhost:1"}, {"--server-url", "http://localhost:1"}} {
		args := append([]string{"inbox"}, flags...)
		if err := runCLI(t.Context(), args, strings.NewReader(""), io.Discard, io.Discard, nil); err == nil || !strings.Contains(err.Error(), "inbox requires local transport") {
			t.Fatalf("accepted incompatible transport %v: %v", flags, err)
		}
	}
}

func TestInboxProfileStateOwnershipAndLoadRollback(t *testing.T) {
	config := minimalConfig(&agentsession.Config{})
	config.Option.Command, config.Option.StateDir = cfg.RunModeInbox, t.TempDir()
	first, err := buildAIScanProfile(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close(context.Background()) })
	if err := first.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := buildAIScanProfile(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close(context.Background()) })
	if err := second.Load(t.Context()); err == nil || !strings.Contains(err.Error(), "already owned") {
		t.Fatalf("second host ownership: %v", err)
	}
	if err := first.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(config.Option.StateDir, "active.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT OR REPLACE INTO kv(key,value) VALUES('router_history',?)", []byte("broken")); err != nil {
		t.Fatal(err)
	}
	broken, err := buildAIScanProfile(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broken.Close(context.Background()) })
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := broken.Load(ctx); err == nil || strings.Contains(err.Error(), "already owned") || ctx.Err() != nil {
		t.Fatalf("corrupt history did not roll back promptly: %v (context=%v)", err, ctx.Err())
	}
	if _, err := db.Exec("DELETE FROM kv WHERE key='router_history'"); err != nil {
		t.Fatal(err)
	}
	recovered, err := buildAIScanProfile(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recovered.Close(context.Background()) })
	if err := recovered.Load(ctx); err != nil {
		t.Fatalf("failed Load retained the ownership lock: %v", err)
	}
}
