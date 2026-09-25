// neutralhost is a test composition of shipped extensions, not a replacement loop.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/namespaces"
	"github.com/chainreactors/cyber/core/telemetry"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	ioaext "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	"github.com/chainreactors/cyber/pkg/exts/native"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	subagentext "github.com/chainreactors/cyber/pkg/exts/subagent"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	"github.com/chainreactors/cyber/pkg/harness"
	"github.com/chainreactors/cyber/pkg/host"
	"github.com/chainreactors/cyber/tools/ioa"
)

func arg(name string) string {
	for i, value := range os.Args {
		if value == name && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}
	return ""
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Minute)
	defer cancel()
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	var config struct {
		LLM struct {
			Providers []struct {
				BaseURL string `json:"base_url"`
				Model   string `json:"model"`
			} `json:"providers"`
		} `json:"llm"`
	}
	raw, err := os.ReadFile(arg("--config"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		return err
	}
	pc := provider.StartupConfig{Mode: provider.StartupDisabled}
	if len(config.LLM.Providers) > 0 {
		entry := config.LLM.Providers[0]
		pc = provider.StartupConfig{Mode: provider.StartupRequired, Config: provider.ProviderConfig{Provider: "openai", BaseURL: entry.BaseURL, APIKey: "harness-gateway-token", Model: entry.Model, MaxTokens: 16384, Timeout: 330}}
	}
	logger := telemetry.GlobalLogger(telemetry.LogConfig{Output: os.Stderr})
	values, err := harness.BaseExtensions(harness.BaseConfig{Directory: wd, Terminal: terminalext.Config{Timeout: 120}, Provider: pc, Logger: logger})
	if err != nil {
		return err
	}
	bundle, diagnostics := ioaext.Skills()
	if len(diagnostics) > 0 {
		return fmt.Errorf("IOA skills: %v", diagnostics)
	}
	se := sessionext.New(session.Config{NodeName: arg("--node-name"), PrimarySessionID: "operator", BaseSkills: []string{"ioa"}, Loop: agent.StandardLoop{}})
	ns := namespaces.New()
	values = append(values, ns, loopext.New(agent.StandardLoop{}), ioaext.New(ioa.Config{URL: arg("--ioa-url"), NodeName: arg("--node-name"), Space: arg("--space"), AutoRegister: true, RegisterCommands: true}), ioaext.NewCollaboration(ioaext.CollaborationOptions{Skills: []skills.Bundle{bundle}}), native.New(), subagentext.New(), se, subagentext.NewTools(), sessionext.NewProtocol())
	set, err := extension.New(values...)
	if err != nil {
		return err
	}
	if err = set.Load(ctx); err != nil {
		_ = set.Close(context.Background())
		return err
	}
	rt := se.Runtime()
	mux := aop.NewNamespaceMux(ctx)
	if err = ns.Bind(mux); err != nil {
		_ = set.Close(context.Background())
		return err
	}

	h := host.New(mux)
	stream := host.NewStdio(os.Stdin, os.Stdout)
	subscription := rt.Observe(func(event *aop.Event) {
		_ = h.Send(aop.Reply("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: event}}), stream.Send)
	})
	err = h.Serve(stream)
	if err != nil {
		cancel()
	}
	rt.WaitOperations()
	closeErr := set.Close(context.Background())
	subscription.Cancel()
	h.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return h.Err()
}
