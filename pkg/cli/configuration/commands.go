// Package configuration exposes cyber-harness configuration commands without
// starting a runtime or depending on any product distribution.
package configuration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
	flags "github.com/jessevdk/go-flags"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

type Check struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}
type Host struct {
	Name     string
	Sections *cfg.Sections
	Context  *cfg.Context
	In       io.Reader
	Out, Err io.Writer
	// Checks are declared by the host, not discovered by loading runtime extensions.
	Checks func(context.Context, *cfg.Option, bool) []Check
}

type initOptions struct {
	Project        bool `long:"project" description:"Create cyber.yaml in the current directory"`
	NonInteractive bool `long:"non-interactive" description:"Generate from arguments without prompting"`
	Force          bool `long:"force" description:"Back up and replace an existing configuration"`
}
type useOptions struct {
	Project bool `long:"project" description:"Save the selection in the project configuration"`
	Args    struct {
		ID string `positional-arg-name:"id" required:"true"`
	} `positional-args:"true"`
}
type configOptions struct {
	Show struct {
		Sources bool `long:"sources" description:"Include field sources"`
	} `command:"show" description:"Show effective configuration (secrets redacted)"`
	Validate struct{}   `command:"validate" description:"Validate configuration without network access"`
	Profiles struct{}   `command:"profiles" description:"List model profiles"`
	Use      useOptions `command:"use" description:"Persist an active profile selection"`
}
type doctorOptions struct {
	Online bool `long:"online" description:"Also test configured network connections"`
}
type commandOptions struct {
	cfg.LLMOptions
	Config        string        `short:"c" long:"config" description:"Use only this configuration file"`
	DataDir       string        `long:"data-dir" description:"Override the data directory"`
	WorkDir       string        `long:"workdir" description:"Project working directory"`
	JSON          bool          `long:"json" description:"Print machine-readable JSON"`
	Init          initOptions   `command:"init" description:"Initialize user configuration; --project for this directory"`
	Configuration configOptions `command:"config" description:"Inspect and manage cyber-harness configuration"`
	Doctor        doctorOptions `command:"doctor" description:"Check configuration and host dependencies"`
}

// RegisterHelp makes the shared command tree visible in a host's root help.
// Run dispatches these commands before ordinary runtime resolution.
func RegisterHelp(parser *flags.Parser) {
	_, _ = parser.AddCommand("init", "Initialize cyber-harness configuration", "", &initOptions{})
	_, _ = parser.AddCommand("config", "Inspect and manage cyber-harness configuration", "", &configOptions{})
	_, _ = parser.AddCommand("doctor", "Check configuration and host dependencies", "", &doctorOptions{})
}

func selected(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		key, _, inline := strings.Cut(arg, "=")
		if key == "--init" {
			return "init", true
		}
		if !strings.HasPrefix(arg, "-") {
			return arg, false
		}
		switch key {
		case "--config", "-c", "--workdir", "--data-dir", "--profile", "--provider", "--model", "--base-url", "--api-key", "--llm-proxy", "--max-tokens", "--context-window":
			if !inline {
				i++
			}
		case "--json":
		default:
			return "", false
		}
	}
	return "", false
}

func Run(ctx context.Context, args []string, host Host) (bool, error) {
	name, legacy := selected(args)
	if name != "init" && name != "config" && name != "doctor" {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if host.In == nil {
		host.In = os.Stdin
	}
	if host.Out == nil {
		host.Out = os.Stdout
	}
	if host.Err == nil {
		host.Err = os.Stderr
	}
	if host.Sections == nil {
		host.Sections = cfg.NewSections()
	}
	if legacy {
		args = append([]string(nil), args...)
		for i, a := range args {
			if a == "--init" {
				args[i] = "init"
				break
			}
		}
		args = append(args, "--non-interactive")
		fmt.Fprintf(host.Err, "--init is deprecated; use %s init --project --non-interactive\n", host.Name)
	}
	var parsed commandOptions
	parser := flags.NewParser(&parsed, flags.Default&^flags.PrintErrors)
	parser.Name = host.Name
	if _, err := parser.ParseArgs(args); err != nil {
		if e, ok := err.(*flags.Error); ok && e.Type == flags.ErrHelp {
			parser.WriteHelp(host.Out)
			return true, nil
		}
		return true, err
	}
	if legacy && parsed.Config == "" {
		parsed.Init.Project = true
	}
	var environment cfg.Context
	if host.Context != nil {
		environment = *host.Context
	}
	if parsed.WorkDir != "" {
		environment.Directory = parsed.WorkDir
	}
	option := cfg.Option{LLMOptions: parsed.LLMOptions, Sections: host.Sections, Context: &environment}
	option.ConfigFile = parsed.Config
	option.DataDir = parsed.DataDir
	cfg.CaptureExplicitFlags(&option, parser)
	if name == "init" {
		return true, initialize(ctx, host, &option, parsed.Init, parsed.JSON)
	}
	if _, err := cfg.ResolveRuntimeConfig(&option); err != nil {
		return true, err
	}
	for _, d := range option.Snapshot.Diagnostics {
		fmt.Fprintln(host.Err, d)
	}
	if name == "doctor" {
		return true, doctor(ctx, host, &option, parsed.Doctor.Online, parsed.JSON)
	}
	if parser.Active.Active == nil {
		return true, fmt.Errorf("use %s config show, validate, profiles, or use", host.Name)
	}
	switch parser.Active.Active.Name {
	case "show":
		view := cfg.Redact(option.Snapshot.Effective, host.Sections)
		if parsed.Configuration.Show.Sources {
			files := []string{}
			for _, file := range option.Snapshot.Layers {
				files = append(files, file.Path)
			}
			return true, printValue(host.Out, map[string]any{"config": view, "sources": option.Snapshot.Sources, "files": files, "target": option.Snapshot.Target}, parsed.JSON)
		}
		return true, printValue(host.Out, view, parsed.JSON)
	case "validate":
		if err := cfg.Validate(&option); err != nil {
			return true, err
		}
		return true, printValue(host.Out, map[string]any{"valid": true, "target": option.Snapshot.Target, "diagnostics": option.Snapshot.Diagnostics}, parsed.JSON)
	case "profiles":
		profiles := []map[string]any{}
		for _, p := range option.Providers {
			profiles = append(profiles, map[string]any{"id": p.ID, "model": p.Model, "provider": p.Provider, "active": p.ID == option.ActiveProfile, "source": option.Snapshot.Sources["llm.providers."+p.ID+".id"]})
		}
		return true, printValue(host.Out, profiles, parsed.JSON)
	case "use":
		return true, useProfile(host, &option, parsed.Configuration.Use, parsed.JSON)
	}
	return true, nil
}

func printValue(out io.Writer, value any, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(out).Encode(value)
	}
	data, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	return err
}

func target(option *cfg.Option, project bool) (string, error) {
	if project && option.ConfigFile != "" {
		return "", fmt.Errorf("--project and --config cannot be combined")
	}
	s, err := cfg.Discover(option.Context, option.ConfigFile)
	if err != nil {
		return "", err
	}
	if option.ConfigFile != "" {
		return s.Target, nil
	}
	if project {
		return filepath.Join(s.Context.Directory, cfg.DefaultConfigName), nil
	}
	return s.Context.UserFile(), nil
}

func initialize(ctx context.Context, host Host, option *cfg.Option, options initOptions, asJSON bool) error {
	path, err := target(option, options.Project)
	if err != nil {
		return err
	}
	if options.Project && option.APIKey != "" {
		return fmt.Errorf("project init does not store credentials; use user init or environment variables")
	}
	if _, err := os.Lstat(path); err == nil && !options.Force {
		return printValue(host.Out, map[string]any{"path": path, "created": false, "message": "configuration already exists; kept unchanged"}, asJSON)
	}
	input, terminal := host.In.(*os.File)
	interactive := terminal && term.IsTerminal(int(input.Fd())) && !options.NonInteractive && !options.Project && !asJSON
	if interactive {
		reader := bufio.NewReader(host.In)
		ask := func(label, current string) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			fmt.Fprintf(host.Err, "%s [%s]: ", label, current)
			v, e := reader.ReadString('\n')
			if e != nil {
				return "", fmt.Errorf("initialization canceled: %w", e)
			}
			v = strings.TrimSpace(v)
			if v == "" {
				v = current
			}
			return v, nil
		}
		if option.Provider == "" {
			option.Provider = "openai"
		}
		if option.Provider, err = ask("Protocol", option.Provider); err != nil {
			return err
		}
		if option.BaseURL, err = ask("API base URL (empty uses provider default)", option.BaseURL); err != nil {
			return err
		}
		if option.Model, err = ask("Model", option.Model); err != nil {
			return err
		}
		if option.Model == "" {
			return fmt.Errorf("model is required for interactive initialization")
		}
		fmt.Fprint(host.Err, "API key (hidden; Enter keeps environment-based credentials): ")
		secret, e := term.ReadPassword(int(input.Fd()))
		fmt.Fprintln(host.Err)
		if e != nil {
			return fmt.Errorf("initialization canceled")
		}
		if len(secret) > 0 {
			option.APIKey = string(secret)
		}
	}
	data, err := cfg.InitialConfig(option)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	created, err := cfg.WriteFile(path, data, options.Force, options.Force)
	if err != nil {
		return err
	}
	return printValue(host.Out, map[string]any{"path": path, "created": created}, asJSON)
}

func useProfile(host Host, option *cfg.Option, options useOptions, asJSON bool) error {
	id := options.Args.ID
	found := false
	for _, p := range option.Providers {
		found = found || p.ID == id
	}
	if !found {
		return fmt.Errorf("unknown LLM profile %q", id)
	}
	path, err := target(option, options.Project)
	if err != nil {
		return err
	}
	if options.Project {
		for _, layer := range option.Snapshot.Layers {
			if layer.Scope == "project" {
				path = layer.Path
			}
		}
	}
	doc := map[string]any{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err = yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("invalid configuration: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	llm, _ := doc["llm"].(map[string]any)
	if llm == nil {
		llm = map[string]any{}
		doc["llm"] = llm
	}
	llm["active_profile"] = id
	data, err = yaml.Marshal(doc)
	if err != nil {
		return err
	}
	environment := option.Snapshot.Context
	environment.Replacements = map[string][]byte{path: data}
	candidate := cfg.Option{Context: &environment, Sections: host.Sections, Explicit: map[string]bool{}}
	candidate.ConfigFile = option.ConfigFile
	if !options.Project && option.ConfigFile == "" {
		candidate.ConfigFile = path
	}
	if _, err = cfg.ResolveRuntimeConfig(&candidate); err != nil {
		return fmt.Errorf("selection is not valid in this scope (use --project for a project profile): %w", err)
	}
	if _, err = cfg.WriteFile(path, data, true, false); err != nil {
		return err
	}
	resolved := cfg.Option{Sections: host.Sections, Context: option.Context}
	resolved.ConfigFile = option.ConfigFile
	if _, err = cfg.ResolveRuntimeConfig(&resolved); err != nil {
		return err
	}
	if resolved.ActiveProfile != id {
		fmt.Fprintf(host.Err, "saved profile %s is overridden by %s\n", id, resolved.Snapshot.Sources["llm.active_profile"])
	}
	return printValue(host.Out, map[string]any{"path": path, "profile": id, "active": resolved.ActiveProfile}, asJSON)
}

func doctor(ctx context.Context, host Host, option *cfg.Option, online, asJSON bool) error {
	checks := []Check{}
	validation := cfg.Validate(option)
	checks = append(checks, Check{"configuration", validation == nil, func() string {
		if validation != nil {
			return validation.Error()
		}
		return "valid"
	}()})
	for name, path := range map[string]string{"data directory": option.DataDir, "configuration directory": filepath.Dir(option.Snapshot.Target)} {
		parent := path
		for {
			_, err := os.Stat(parent)
			if err == nil {
				break
			}
			if !os.IsNotExist(err) || filepath.Dir(parent) == parent {
				break
			}
			parent = filepath.Dir(parent)
		}
		info, err := os.Stat(parent)
		ok := err == nil && info.IsDir()
		checks = append(checks, Check{name, ok, path})
	}
	if online {
		p := cfg.ProviderConfig(option)
		if p.APIKey == "" || p.Model == "" {
			checks = append(checks, Check{"model", true, "not configured; skipped"})
		} else {
			checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			checks = append(checks, probeModel(checkCtx, p))
			cancel()
		}
	}
	if host.Checks != nil {
		checks = append(checks, host.Checks(ctx, option, online)...)
	}
	if err := printValue(host.Out, checks, asJSON); err != nil {
		return err
	}
	for _, check := range checks {
		if !check.OK {
			return fmt.Errorf("configuration checks failed")
		}
	}
	return nil
}

// Keep the model check bounded and protocol-independent via the provider API.
func probeModel(ctx context.Context, config provider.ProviderConfig) Check {
	result, err := provider.TestLLM(ctx, &types.LLMProbeRequest{Provider: config.Provider, BaseUrl: config.BaseURL, ApiKey: config.APIKey, Model: config.Model, Proxy: config.Proxy}, "")
	if err != nil || result == nil || !result.Ok {
		return Check{"model", false, "connection failed; check endpoint, model and credentials"}
	}
	return Check{"model", true, "connection successful"}
}
