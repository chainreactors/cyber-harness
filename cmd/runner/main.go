package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/chainreactors/aiscan/pkg/commands"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	toolnode "github.com/chainreactors/aiscan/pkg/node/tool"

	"github.com/chainreactors/aiscan/tools/files"
)

type options struct {
	server     string
	token      string
	id         string
	websocket  string
	workDir    string
	readOnly   bool
	maxBytes   int64
	jsonFrames bool
	version    bool
	extensions string
	output     string
	skillsDir  string
	discover   bool
}

func main() {
	if code, handled := commands.RunShellCommandProxy(); handled {
		os.Exit(code)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "runner:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) (err error) {
	options, err := parseOptions(args, stderr)
	if err != nil {
		return err
	}
	if options.version {
		fmt.Fprintf(stdout, "runner v%s\n", cfg.Version)
		return nil
	}
	workDir := options.workDir
	if workDir == "" {
		workDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve working directory: %w", err)
		}
	}
	workDir, err = filepath.Abs(workDir)
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}
	logger := telemetry.GlobalLogger(telemetry.LogConfig{
		Output: stderr,
	})
	skillsDir := options.skillsDir
	if skillsDir != "" {
		skillsDir, err = filepath.Abs(skillsDir)
		if err != nil {
			return err
		}
	}
	profile, err := newWorkspaceProfile(workspaceProfileConfig{
		Extensions:      strings.Split(options.extensions, ","),
		Files:           files.Config{Directory: workDir, ReadOnly: options.readOnly, MaxBytes: options.maxBytes},
		Output:          options.output,
		SkillsDirectory: skillsDir,
	})
	if err != nil {
		return err
	}
	if err := profile.Load(ctx); err != nil {
		return errors.Join(err, profile.Close(context.Background()))
	}
	defer func() { err = errors.Join(err, profile.Close(context.Background())) }()
	executor, err := profile.Executor()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(executor.ToolDefinitions()))
	for _, definition := range executor.ToolDefinitions() {
		names = append(names, definition.Name)
	}
	if options.discover {
		return json.NewEncoder(stdout).Encode(struct {
			Available []string `json:"available"`
			Installed []string `json:"installed"`
			Tools     []string `json:"tools"`
			Skills    []string `json:"skills"`
		}{availableWorkspaceExtensions(), profile.Installed(), names, profile.SkillLocations()})
	}
	logger.Infof("runner tools ready: %s", strings.Join(names, ", "))
	return toolnode.Run(ctx, toolnode.Config{
		ServerURL: options.server,
		WSPath:    options.websocket,
		ID:        options.id,
		Token:     options.token,
		Executor:  executor,
		Events:    profile.Events(),
		Logger:    logger,
		Version:   cfg.Version,
		JSON:      options.jsonFrames,
	})
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	var result options
	flags := flag.NewFlagSet("runner", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&result.server, "server", "", "AOP server URL")
	flags.StringVar(&result.token, "token", "", "server access token")
	flags.StringVar(&result.id, "id", "", "stable runner ID (defaults to hostname)")
	flags.StringVar(&result.websocket, "ws-path", toolnode.DefaultWSPath, "AOP WebSocket path")
	flags.StringVar(&result.workDir, "workdir", "", "directory exposed by file tools (default current directory)")
	flags.BoolVar(&result.readOnly, "read-only", false, "disable write")
	flags.Int64Var(&result.maxBytes, "max-file-bytes", 1<<20, "maximum UTF-8 file size")
	flags.BoolVar(&result.jsonFrames, "json", false, "use ProtoJSON WebSocket frames")
	flags.BoolVar(&result.version, "version", false, "print version")
	flags.StringVar(&result.extensions, "extensions", "files", "exact extension selection: files,observe,skills")
	flags.StringVar(&result.output, "output", "", "write the canonical AOP event stream to a new JSONL file")
	flags.StringVar(&result.skillsDir, "skills-dir", "", "read-only directory for the selected skills extension")
	flags.BoolVar(&result.discover, "discover", false, "load extensions, print installed tools and skill paths as JSON, then close")
	if err := flags.Parse(args); err != nil {
		return result, err
	}
	if len(flags.Args()) != 0 {
		return result, fmt.Errorf("unexpected positional arguments")
	}
	if strings.TrimSpace(result.server) == "" && !result.version && !result.discover {
		return result, fmt.Errorf("--server is required")
	}
	return result, nil
}
