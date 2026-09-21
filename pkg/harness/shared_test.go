package harness_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/proc"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/pkg/harness"
)

func TestInstallationSharesCapabilitiesAndRetargetableLogger(t *testing.T) {
	var capturedLogger telemetry.Logger
	var capturedProcesses *proc.Manager
	h, err := harness.New(harness.Config{
		Base:    harness.BaseConfig{Directory: t.TempDir()},
		Session: &session.Config{Loop: agent.NoLoop()},
		Extensions: []extension.Extension{extension.Func{LoadFunc: func(scope *extension.Scope) error {
			var err error
			if capturedLogger, err = extension.Use[telemetry.Logger](scope); err != nil {
				return err
			}
			capturedProcesses, err = extension.Use[*proc.Manager](scope)
			return err
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	if _, err := h.Events(); err == nil {
		t.Fatal("published before load")
	}
	if err := h.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	rt, err := h.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	providers, err := h.Providers()
	if err != nil {
		t.Fatal(err)
	}
	providers.Set(providerStub{}, provider.ProviderConfig{Model: "shared"})
	if _, config := rt.ProviderState(); config.Model != "shared" {
		t.Fatal("runtime has a second provider state")
	}
	processes, err := h.Processes()
	if err != nil {
		t.Fatal(err)
	}
	if processes != capturedProcesses {
		t.Fatal("second process manager")
	}
	stream, err := h.Events()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	sub := rt.Observe(events.ObserverFunc(func(*aop.Event) { count++ }))
	defer sub.Cancel()
	first := &aop.Event{SessionId: "shared", Payload: &aop.Event_TurnStarted{TurnStarted: &aop.TurnStarted{}}}
	second := &aop.Event{SessionId: "shared", Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{}}}
	stream.Publish(first)
	rt.Publish(second)
	if count != 2 || first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("event stream split: count=%d seq=%d,%d", count, first.Seq, second.Seq)
	}
	var logs bytes.Buffer
	rt.Logger.SetOutput(&logs)
	capturedLogger.Infof("shared log destination")
	if !strings.Contains(logs.String(), "shared log destination") {
		t.Fatal("runtime did not retarget shared logger")
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Providers(); err == nil {
		t.Fatal("published after close")
	}
}
