package command_test

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/command"
)

type simpleCommand struct{ name string }

func (c *simpleCommand) Name() string                                          { return c.name }
func (c *simpleCommand) Usage() string                                         { return c.name }
func (c *simpleCommand) Execute(_ context.Context, _ []string) (string, error) { return "ok", nil }

func TestScannerRejectsShellPipeAndFileRedir(t *testing.T) {
	registry := command.NewRegistry()
	registry.Register(&simpleCommand{name: "spray"}, "")
	bash := command.NewBashTool(t.TempDir(), 5, registry)

	tests := []struct {
		name     string
		cmd      string
		wantHint string
	}{
		{"pipe to head", `spray -u http://x | head -30`, "shell pipes"},
		{"double pipe", `spray -u http://x || echo done`, "shell pipes"},
		{"file redirection >", `spray -u http://x > out.txt`, "file redirection"},
		{"file redirection >>", `spray -u http://x >> out.txt`, "file redirection"},
		{"stderr to file", `spray -u http://x 2>err.log`, "file redirection"},
		{"combined to file", `spray -u http://x &> all.log`, "file redirection"},
		{"chained with &&", `spray -u http://x && spray -u http://y`, "chaining"},
		{"chained with ;", `spray -u http://x ; echo done`, "chaining"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := bash.Execute(context.Background(), bashArgs(tt.cmd))
			if err == nil {
				t.Fatalf("expected error, got output %q", res.Text())
			}
			if !strings.Contains(err.Error(), tt.wantHint) {
				t.Fatalf("error = %v, want hint containing %q", err, tt.wantHint)
			}
		})
	}
}

func TestBashProxyEnvInjection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	proxy := "socks5://127.0.0.1:1080"
	bash := command.NewBashTool(t.TempDir(), 5, nil).WithScannerProxy(proxy)

	res, err := bash.Execute(context.Background(), bashArgs("env"))
	if err != nil {
		t.Fatalf("bash env: %v", err)
	}
	out := res.Text()
	for _, envVar := range []string{"ALL_PROXY", "all_proxy", "HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		if !strings.Contains(out, envVar+"="+proxy) {
			t.Errorf("env output missing %s", envVar)
		}
	}
}

func TestBashNoProxyEnvWhenEmpty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	bash := command.NewBashTool(t.TempDir(), 5, nil)

	res, err := bash.Execute(context.Background(), bashArgs("env"))
	if err != nil {
		t.Fatalf("bash env: %v", err)
	}
	if strings.Contains(res.Text(), "ALL_PROXY=socks5://") {
		t.Errorf("should not inject proxy when empty")
	}
}

func bashArgs(cmd string) string {
	data, _ := json.Marshal(map[string]string{"command": cmd})
	return string(data)
}
