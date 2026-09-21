package configuration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cfg "github.com/chainreactors/cyber/pkg/config"
)

func commandHost(t *testing.T) (Host, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "project")
	_ = os.MkdirAll(directory, 0700)
	var out bytes.Buffer
	return Host{Name: "fixture", Context: &cfg.Context{Home: filepath.Join(root, "user"), Directory: directory, Executable: filepath.Join(root, "bin", "fixture"), LookupEnv: func(string) (string, bool) { return "", false }}, In: strings.NewReader(""), Out: &out, Err: &out}, &out
}
func runCommand(t *testing.T, host Host, args ...string) {
	t.Helper()
	handled, err := Run(context.Background(), args, host)
	if !handled || err != nil {
		t.Fatalf("%v: handled=%v error=%v", args, handled, err)
	}
}

func TestInitDefaultsToUserAndNeverOverwrites(t *testing.T) {
	host, out := commandHost(t)
	runCommand(t, host, "init", "--non-interactive", "--provider", "openai", "--model", "one")
	path := host.Context.UserFile()
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	runCommand(t, host, "init", "--model", "two")
	second, _ := os.ReadFile(path)
	if !bytes.Equal(first, second) {
		t.Fatal("repeat init overwrote configuration")
	}
	runCommand(t, host, "init", "--force", "--model", "two")
	backups, _ := filepath.Glob(path + ".bak-*")
	if len(backups) != 1 {
		t.Fatal("force did not back up")
	}
	if strings.Contains(out.String(), "api_key:") {
		t.Fatal("init printed credentials")
	}
}

func TestProjectInitAndExplicitPath(t *testing.T) {
	host, _ := commandHost(t)
	runCommand(t, host, "init", "--project")
	if _, err := os.Stat(filepath.Join(host.Context.Directory, cfg.DefaultConfigName)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(host.Context.UserFile()); !os.IsNotExist(err) {
		t.Fatal("project init wrote global config")
	}
	runCommand(t, host, "-c", "custom.yaml", "init", "--model", "fixture")
	if _, err := os.Stat(filepath.Join(host.Context.Directory, "custom.yaml")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "--project", "--api-key", "secret"}, {"init", "--project", "-c", "other.yaml"}} {
		if _, err := Run(t.Context(), args, host); err == nil {
			t.Fatal("ambiguous initialization accepted")
		}
	}
}

func TestShowSourcesRedactsCredentialsAndDoesNotMutate(t *testing.T) {
	host, out := commandHost(t)
	runCommand(t, host, "init", "--model", "fixture", "--api-key", "private-value", "--base-url", "https://username:password@example.test/v1?api_key=hidden")
	before, _ := os.ReadFile(host.Context.UserFile())
	out.Reset()
	runCommand(t, host, "config", "show", "--sources", "--json")
	if strings.Contains(out.String(), "private-value") || strings.Contains(out.String(), "username") || strings.Contains(out.String(), "password") || strings.Contains(out.String(), "hidden") {
		t.Fatalf("configuration output leaked a secret")
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["sources"] == nil {
		t.Fatal("no source diagnostics")
	}
	after, _ := os.ReadFile(host.Context.UserFile())
	if !bytes.Equal(before, after) {
		t.Fatal("show changed configuration")
	}
}

func TestConfigurationCommandDispatchDoesNotStealPrompts(t *testing.T) {
	host, _ := commandHost(t)
	for _, args := range [][]string{{"-p", "init"}, {"agent", "-p", "config"}, {"scan", "-i", "doctor"}, {"--version"}} {
		handled, err := Run(t.Context(), args, host)
		if handled || err != nil {
			t.Fatalf("stole %v", args)
		}
	}
	runCommand(t, host, "--init")
	if _, err := os.Stat(filepath.Join(host.Context.Directory, cfg.DefaultConfigName)); err != nil {
		t.Fatal(err)
	}
	runCommand(t, host, "-c", "legacy-custom.yaml", "--init")
	if _, err := os.Stat(filepath.Join(host.Context.Directory, "legacy-custom.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestUseProfilePreservesSecretAndProjectOverride(t *testing.T) {
	host, out := commandHost(t)
	path := host.Context.UserFile()
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	body := "llm:\n  active_profile: a\n  providers:\n    - id: a\n      model: one\n      api_key: hidden-secret\n    - id: b\n      model: two\n"
	_ = os.WriteFile(path, []byte(body), 0600)
	project := filepath.Join(host.Context.Directory, cfg.DefaultConfigName)
	_ = os.WriteFile(project, []byte("llm:\n  active_profile: a\n"), 0600)
	runCommand(t, host, "config", "use", "b")
	if !strings.Contains(out.String(), "overridden") {
		t.Fatal("missing override notice")
	}
	runCommand(t, host, "config", "use", "b", "--project")
	data, _ := os.ReadFile(project)
	if strings.Contains(string(data), "hidden-secret") {
		t.Fatal("copied inherited secret into project")
	}
}

func TestDoctorOfflineDoesNotInvokeNetwork(t *testing.T) {
	host, _ := commandHost(t)
	calls := 0
	host.Checks = func(_ context.Context, _ *cfg.Option, online bool) []Check {
		if online {
			calls++
		}
		return nil
	}
	runCommand(t, host, "doctor")
	if calls != 0 {
		t.Fatal("offline doctor used online checks")
	}
}

func TestCancelledInitDoesNotWrite(t *testing.T) {
	host, _ := commandHost(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if handled, err := Run(ctx, []string{"init", "--non-interactive"}, host); !handled || err == nil {
		t.Fatal("canceled initialization was accepted")
	}
	if _, err := os.Stat(host.Context.UserFile()); !os.IsNotExist(err) {
		t.Fatal("canceled initialization wrote a file")
	}
}
