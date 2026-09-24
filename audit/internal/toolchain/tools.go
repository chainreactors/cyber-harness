// Package toolchain provisions audit's required CLIs through the shared CRTM installer.
package toolchain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	crtm "github.com/chainreactors/crtm/pkg"
)

type Spec struct{ Name, Version string }

var Required = requiredTools()

func requiredTools() []Spec {
	var tools []Spec
	for name, version := range ToolSpec.Tools {
		tools = append(tools, Spec{name, version})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

// Options is shared by preflight and the session's arsenal commands.
func Options() (crtm.ManagerOption, error) {
	bundle, err := EmbeddedBundle()
	return ToolSpec.ManagerOption(bundle), err
}

type Status struct {
	Name    string `json:"name"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

type Manager struct {
	manager *crtm.Manager
	bundle  *crtm.Bundle
	// Test seams are private; production always executes and installs real tools.
	lookup  func(string) (string, error)
	probe   func(context.Context, Spec, string) (string, error)
	install func(context.Context, string, string, func(context.Context, string) error) error
}

func New(dataDir string) (*Manager, error) {
	options, err := Options()
	if err != nil {
		return nil, err
	}
	options.BinPath, options.ConfigPath = filepath.Join(dataDir, "arsenal", "bin"), filepath.Join(dataDir, "arsenal", "cyber.yaml")
	manager, err := crtm.NewManager(options)
	if err != nil {
		return nil, err
	}
	bundle, _ := options.Sources[0].(*crtm.Bundle)
	return &Manager{manager: manager, bundle: bundle, lookup: exec.LookPath, probe: Probe, install: manager.InstallVersionContext}, nil
}
func (m *Manager) BinDir() string { return m.manager.BinPath() }

// Check is read-only and never resolves releases, downloads or creates directories.
func (m *Manager) Check(ctx context.Context) []Status {
	result := make([]Status, 0, len(Required))
	for _, spec := range Required {
		result = append(result, m.check(ctx, spec))
	}
	return result
}
func (m *Manager) check(ctx context.Context, spec Spec) Status {
	status := Status{Name: spec.Name}
	shared := filepath.Join(m.BinDir(), crtm.BinaryName(spec.Name))
	candidate := shared
	if _, err := os.Stat(shared); os.IsNotExist(err) {
		candidate, err = m.lookup(spec.Name)
		if err != nil {
			status.Error = "missing"
			return status
		}
	}
	status.Path = candidate
	version, err := m.probe(ctx, spec, candidate)
	status.Version = version
	if err != nil {
		status.Error = err.Error()
	}
	return status
}

// Ensure finishes before provider startup, and does not download compatible tools.
func (m *Manager) Ensure(ctx context.Context, out io.Writer) ([]Status, error) {
	if err := m.manager.Prepare(ctx, m.bundle); err != nil {
		return nil, err
	}
	var statuses []Status
	for _, spec := range Required {
		if err := ctx.Err(); err != nil {
			return statuses, err
		}
		status := m.check(ctx, spec)
		if status.Error != "" {
			fmt.Fprintf(out, "Installing %s %s (%s)\n", spec.Name, spec.Version, status.Error)
			err := m.install(ctx, spec.Name, spec.Version, func(ctx context.Context, path string) error {
				version, err := m.probe(ctx, spec, path)
				if err != nil {
					return err
				}
				if version != spec.Version {
					return fmt.Errorf("expected %s, got %s", spec.Version, version)
				}
				return nil
			})
			if err != nil {
				return statuses, fmt.Errorf("required tool %s: %w; provision with cyber-audit tools install", spec.Name, err)
			}
			status = m.check(ctx, spec)
			if status.Error != "" {
				return statuses, fmt.Errorf("required tool %s: %s", spec.Name, status.Error)
			}
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

var versionPattern = regexp.MustCompile(`(?:^|[^0-9])([0-9]+\.[0-9]+\.[0-9]+)(?:[^0-9]|$)`)

func versionParts(value string) [3]int {
	var result [3]int
	for i, part := range strings.Split(value, ".") {
		if i < 3 {
			result[i], _ = strconv.Atoi(part)
		}
	}
	return result
}
func compatible(actual, minimum string) bool {
	a, b := versionParts(actual), versionParts(minimum)
	if a[0] != b[0] || a[0] == 0 && a[1] != b[1] {
		return false
	}
	for i := range a {
		if a[i] > b[i] {
			return true
		}
		if a[i] < b[i] {
			return false
		}
	}
	return true
}
func command(ctx context.Context, path, input string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = os.TempDir()
	cmd.Stdin = strings.NewReader(input)
	// Isolate ripgrep's probe from user-supplied argument configuration.
	cmd.Env = append(os.Environ(), "RIPGREP_CONFIG_PATH=")
	return cmd.CombinedOutput()
}

// Probe checks versions and the specific interfaces used by audit skills, offline.
func Probe(ctx context.Context, spec Spec, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	output, err := command(ctx, path, "", "--version")
	if err != nil {
		return "", fmt.Errorf("version check: %w", err)
	}
	match := versionPattern.FindStringSubmatch(string(output))
	if len(match) != 2 {
		return "", fmt.Errorf("unrecognized version: %.160s", output)
	}
	version := match[1]
	if !compatible(version, spec.Version) {
		return version, fmt.Errorf("incompatible %s; need >= %s within the same %s", version, spec.Version, map[bool]string{true: "minor version", false: "major version"}[versionParts(spec.Version)[0] == 0])
	}
	switch spec.Name {
	case "rg":
		output, err = command(ctx, path, "audit_probe\n", "--json", "--fixed-strings", "audit_probe", "-")
		if err == nil && !bytes.Contains(output, []byte(`"type":"match"`)) {
			err = errors.New("missing JSON match output")
		}
	case "ast-grep":
		output, err = command(ctx, path, "console.log('audit_probe');\n", "run", "--lang", "ts", "--pattern", "console.log($$$ARGS)", "--json", "--stdin")
		var matches []struct {
			Text string `json:"text"`
		}
		if err == nil {
			err = json.Unmarshal(output, &matches)
			if err == nil && (len(matches) == 0 || !strings.Contains(matches[0].Text, "audit_probe")) {
				err = errors.New("missing structural match")
			}
		}
	case "osv-scanner":
		output, err = command(ctx, path, "", "scan", "source", "--help")
		if err == nil {
			for _, flag := range []string{"--format", "--recursive", "--lockfile", "--no-call-analysis", "--output-file"} {
				if !bytes.Contains(output, []byte(flag)) {
					err = fmt.Errorf("missing %s", flag)
					break
				}
			}
		}
	default:
		return version, fmt.Errorf("unknown required tool %s", spec.Name)
	}
	if err != nil {
		return version, fmt.Errorf("capability check: %w", err)
	}
	return version, nil
}
