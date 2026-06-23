package found_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tmux "github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/commands"
	foundcmd "github.com/chainreactors/aiscan/pkg/tools/found"
)

func newBashWithFound(dir string) *commands.BashTool {
	registry := commands.NewRegistry()
	cmd := foundcmd.New()
	cmd.SetWorkDir(dir)
	registry.Register(cmd, "found")

	bash := commands.NewBashTool(dir, 30)
	bash.Manager().SetCommands(func(name string) (tmux.Command, bool) {
		return registry.Get(name)
	})
	bash.Manager().SetExecHooks(
		func(w io.Writer) { commands.Output.Reset(w) },
		func() { commands.Output.Reset(nil) },
	)
	bash.Manager().SetWorkDir(dir)
	return bash
}

func bashArgs(cmd string) string {
	data, _ := json.Marshal(map[string]string{"command": cmd})
	return string(data)
}

func TestFoundScanFile(t *testing.T) {
	dir := t.TempDir()
	// Create a test file with secrets
	content := `# config
AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE
AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
DB_HOST=localhost
DB_PASSWORD="super_secret_password_123"
GITHUB_TOKEN=ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij
STRIPE_KEY=sk_live_ABCDEFGHIJKLMNOPQRSTUVWXYZab
`
	if err := os.WriteFile(filepath.Join(dir, "config.env"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	bash := newBashWithFound(dir)
	res, err := bash.Execute(context.Background(), bashArgs("found -i "+filepath.Join(dir, "config.env")))
	if err != nil {
		t.Fatalf("found error: %v", err)
	}
	out := res.Text()
	t.Logf("output:\n%s", out)

	if !strings.Contains(out, "aws") && !strings.Contains(out, "AWS") {
		t.Error("should detect AWS key")
	}
	if !strings.Contains(out, "findings") {
		t.Error("should show findings summary")
	}
}

func TestFoundScanDir(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "src")
	os.MkdirAll(subdir, 0755)

	os.WriteFile(filepath.Join(subdir, "app.py"), []byte(`
import os
API_KEY = "sk_live_4eC39HqLyjWDarjtT1zdp7dc"
SLACK_TOKEN = "xoxb-1234567890-abcdefghij"
`), 0644)

	os.WriteFile(filepath.Join(subdir, "db.yaml"), []byte(`
database:
  host: 10.0.0.5:3306
  url: mysql://admin:p@ssw0rd@10.0.0.5:3306/mydb
`), 0644)

	bash := newBashWithFound(dir)
	res, err := bash.Execute(context.Background(), bashArgs("found -i "+dir))
	if err != nil {
		t.Fatalf("found error: %v", err)
	}
	out := res.Text()
	t.Logf("output:\n%s", out)

	if !strings.Contains(out, "findings") {
		t.Error("should show findings")
	}
}

func TestFoundExpression(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(`
Meeting notes:
  - Production API at https://admin:hunter2@api.example.com/v1
  - Backup at 10.0.1.5:8080
  - Token: AKIA1234567890ABCDEF
`), 0644)

	bash := newBashWithFound(dir)
	res, err := bash.Execute(context.Background(), bashArgs(`found -i `+dir+` -e "AKIA[A-Z0-9]{16}"`))
	if err != nil {
		t.Fatalf("found error: %v", err)
	}
	out := res.Text()
	t.Logf("output:\n%s", out)

	if !strings.Contains(out, "AKIA1234567890ABCDEF") {
		t.Error("should match custom expression")
	}
}

func TestFoundPipeFromEcho(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	dir := t.TempDir()
	bash := newBashWithFound(dir)

	// echo with secrets | found
	res, err := bash.Execute(context.Background(), bashArgs(
		`echo -e "AWS_KEY=AKIAIOSFODNN7EXAMPLE\nGITHUB=ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij\nNORMAL=hello" | found`))
	if err != nil {
		t.Fatalf("pipe error: %v", err)
	}
	out := res.Text()
	t.Logf("output:\n%s", out)

	if !strings.Contains(out, "AKIA") || !strings.Contains(out, "ghp_") {
		t.Error("should detect secrets from piped input")
	}
}

func TestFoundPipeFromCat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "secrets.txt")
	os.WriteFile(secretFile, []byte(`
sk_live_ABCDEFGHIJKLMNOPQRSTUVWXYZab
ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij
mysql://root:password@localhost:3306/db
`), 0644)

	bash := newBashWithFound(dir)
	res, err := bash.Execute(context.Background(), bashArgs("cat "+secretFile+" | found"))
	if err != nil {
		t.Fatalf("pipe error: %v", err)
	}
	out := res.Text()
	t.Logf("output:\n%s", out)

	if !strings.Contains(out, "findings") {
		t.Error("should detect secrets from catted file")
	}
}

func TestFoundPipeThenGrep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mixed.txt"), []byte(`
AKIAIOSFODNN7EXAMPLE
ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij
sk_live_ABCDEFGHIJKLMNOPQRSTUVWXYZab
nothing_here
`), 0644)

	bash := newBashWithFound(dir)
	// found -i file | grep critical
	res, err := bash.Execute(context.Background(), bashArgs("found -i "+filepath.Join(dir, "mixed.txt")+" | grep critical"))
	if err != nil {
		t.Fatalf("pipe error: %v", err)
	}
	out := strings.TrimSpace(res.Text())
	t.Logf("output:\n%s", out)

	for _, line := range strings.Split(out, "\n") {
		if line != "" && !strings.Contains(line, "critical") {
			t.Errorf("line should contain 'critical': %q", line)
		}
	}
}

func TestFoundJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "env"), []byte(`TOKEN=ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij`), 0644)

	bash := newBashWithFound(dir)
	res, err := bash.Execute(context.Background(), bashArgs("found -i "+filepath.Join(dir, "env")+" -j"))
	if err != nil {
		t.Fatalf("found error: %v", err)
	}
	out := res.Text()
	t.Logf("output:\n%s", out)

	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" || strings.HasPrefix(line, "[found]") {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("line should be valid JSON: %q, err: %v", line, err)
		}
	}
}
