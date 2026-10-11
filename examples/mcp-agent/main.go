// Command mcp-agent runs a real model with MCP commands in a persistent bash host.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/extension"
	loopext "github.com/chainreactors/cyber/exts/agent"
	mcpext "github.com/chainreactors/cyber/exts/mcp"
	sessionext "github.com/chainreactors/cyber/exts/session"
	terminalext "github.com/chainreactors/cyber/exts/terminal"
	"github.com/chainreactors/cyber/pkg/harness"
	"google.golang.org/protobuf/encoding/protojson"
)

func main() {
	configPath := flag.String("config", "", "MCP JSON config file (mcpServers)")
	taskPath := flag.String("task", "", "UTF-8 task file")
	directory := flag.String("dir", "", "isolated task working directory")
	tracePath := flag.String("trace", "", "optional new AOP JSONL trace file")
	baseURL := flag.String("base-url", "", "OpenAI-compatible API base URL")
	model := flag.String("model", "", "model ID")
	maxTurns := flag.Int("max-turns", 40, "maximum model decisions")
	timeout := flag.Duration("timeout", 15*time.Minute, "whole task deadline")
	flag.Parse()
	// Hold transport credentials in memory before creating any tool or child.
	key := os.Getenv("CYBER_MCP_API_KEY")
	err := os.Unsetenv("CYBER_MCP_API_KEY")
	if err == nil && (key == "" || *configPath == "" || *taskPath == "" || *directory == "" || *model == "" || *baseURL == "" || *maxTurns <= 0 || *timeout <= 0) {
		err = fmt.Errorf("require CYBER_MCP_API_KEY, -config, -task, -dir, -base-url and -model; limits must be positive")
	}
	redact := func(value string) string {
		if key == "" {
			return value
		}
		return strings.ReplaceAll(value, key, "[redacted]")
	}
	if err == nil {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		err = run(ctx, *configPath, *taskPath, *directory, *tracePath, *maxTurns,
			provider.ProviderConfig{Provider: "openai", BaseURL: *baseURL, APIKey: key, Model: *model, MaxTokens: 8192, Timeout: 180}, redact)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, redact(err.Error()))
		os.Exit(1)
	}
}

func run(ctx context.Context, configPath, taskPath, directory, tracePath string, maxTurns int, llm provider.ProviderConfig, redact func(string) string) (resultErr error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var config mcpext.Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF || config.MCPServers == nil {
		return fmt.Errorf("MCP config requires one JSON object with mcpServers")
	}
	task, err := os.ReadFile(taskPath)
	if err != nil {
		return err
	}
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return fmt.Errorf("task directory must already exist")
	}
	var trace *os.File
	if tracePath != "" {
		trace, err = os.OpenFile(tracePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, trace.Close()) }()
	}
	// Select the existing loop and session extensions without delegation tools.
	sessions := sessionext.New(agentsession.Config{PrimarySessionID: "main"})
	h, err := harness.New(harness.Config{
		Base: harness.BaseConfig{
			Directory: directory, SkillExclude: []string{"cyber"},
			Terminal: terminalext.Config{Timeout: 90},
			Provider: provider.StartupConfig{Mode: provider.StartupRequired, Config: llm},
		},
		Extensions: []extension.Extension{mcpext.New(config), loopext.New(agent.StandardLoop{}), sessions},
	})
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		closeErr := h.Close(closeCtx)
		fmt.Fprintf(os.Stderr, "harness_closed=%t\n", closeErr == nil)
		resultErr = errors.Join(resultErr, closeErr)
	}()
	if err := h.Load(ctx); err != nil {
		return err
	}
	runtime := sessions.Runtime()
	var mu sync.Mutex
	var traceErr error
	calls, failures := 0, 0
	sub := runtime.Observe(func(event *aop.Event) {
		// Completed events retain the task and evidence without per-token deltas.
		if event.GetMessageDelta() != nil || event.GetToolCallDelta() != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if trace != nil && traceErr == nil {
			encoded, err := protojson.Marshal(event)
			if err == nil {
				_, err = fmt.Fprintln(trace, redact(string(encoded)))
			}
			traceErr = err
		}
		if call := event.GetToolCall(); call != nil {
			calls++
			fmt.Fprintf(os.Stderr, "tool[%d] %s %s\n", calls, call.Name, redact(string(call.GetArguments().GetData())))
		}
		if result := event.GetToolResult(); result != nil && result.IsError {
			failures++
			fmt.Fprintf(os.Stderr, "tool_error call=%s\n", result.CallId)
		}
	})
	defer func() { resultErr = errors.Join(resultErr, sub.Close(context.Background())) }()
	session, err := runtime.OpenSession(ctx, agentsession.SessionOptions{ID: "main"})
	if err != nil {
		return err
	}
	started := time.Now()
	turn, err := session.Run(ctx, agentsession.RunInput{Content: []*aop.Content{aop.Text(string(task))}, MaxTurns: maxTurns})
	if err != nil {
		return err
	}
	result, runErr := turn.Wait()
	if result != nil {
		fmt.Println(redact(result.Output))
		mu.Lock()
		metrics, err := json.Marshal(map[string]any{"model": llm.Model, "elapsed_seconds": time.Since(started).Seconds(), "decisions": result.Turns, "tool_calls": calls, "tool_errors": failures, "stop": result.Stop, "usage": result.TotalUsage})
		traceErr = errors.Join(traceErr, err)
		mu.Unlock()
		fmt.Fprintln(os.Stderr, string(metrics))
		if runErr == nil && result.Stop != agent.StopReasonCompleted {
			runErr = fmt.Errorf("task did not complete: %s", result.Stop)
		}
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	reason := agentsession.SessionCloseCompleted
	if runErr != nil {
		reason = agentsession.SessionCloseError
	}
	closeErr := runtime.CloseSession(closeCtx, session.ID(), reason)
	mu.Lock()
	defer mu.Unlock()
	return errors.Join(runErr, closeErr, traceErr)
}
