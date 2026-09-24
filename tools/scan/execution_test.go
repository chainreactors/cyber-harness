package scan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/operation"
	"github.com/chainreactors/cyber/tools/scan/engine"
	"github.com/chainreactors/sdk/spray"
	"github.com/chainreactors/utils/parsers"
)

func TestExecutionOnlyRejectsAIModesBeforeExecuting(t *testing.T) {
	command := New(nil, WithExecutionOnly())
	for _, args := range [][]string{{"--sniper"}, {"--verify", "on"}} {
		_, _, err := command.execute(context.Background(), args, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "AI modes are unavailable") {
			t.Fatalf("%v: %v", args, err)
		}
	}
	_, _, err := New(nil, WithExecutionOnly(), WithVerification("on", nil)).execute(t.Context(), nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "require a configured model") {
		t.Fatalf("execution-only node ignored configured verification: %v", err)
	}
}

func TestVerificationPrecedenceAndEvidenceOnFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "definitely-not-present") // Matches only the local test template.
	}))
	defer server.Close()
	for _, test := range []struct {
		name, node, explicit, failure string
		model, verify                 bool
	}{
		{name: "no model"},
		{name: "model default", model: true, verify: true},
		{name: "node off", node: "off", model: true},
		{name: "invocation off", node: "on", explicit: "off", model: true},
		{name: "invocation on", node: "off", explicit: "on", model: true, verify: true},
		{name: "canceled during verification", model: true, verify: true, failure: "cancel"},
		{name: "model failure", model: true, verify: true, failure: "auth"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sprayEngine, err := spray.NewEngine(nil)
			if err != nil {
				t.Fatal(err)
			}
			engines := &engine.Set{Spray: sprayEngine, Neutron: newScanTestNeutronEngine(t, scanTestTemplate("local-evidence"))}
			defer engines.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			ctx = operation.ContextWithInvocation(ctx, operation.Invocation{CallID: "evidence-call", SessionID: "evidence-session", Emitter: "scan"})
			stream := coreevents.New()
			var events []*aop.Event
			sub := stream.Observe(coreevents.ObserverFunc(func(event *aop.Event) { events = append(events, event) }))
			defer sub.Cancel()
			calls := 0
			command := New(engines, WithEvents(stream), WithVerification(test.node, func(context.Context) bool { return test.model }), WithWorker(func(ctx context.Context, _ string, loot parsers.Loot) (string, error) {
				calls++
				if loot.Kind != parsers.LootVuln {
					t.Errorf("unexpected verification candidate: %s", loot.Kind)
				}
				if test.failure == "cancel" {
					cancel()
					return "", ctx.Err()
				}
				if test.failure == "auth" {
					return "status:confirmed", errors.New("authentication failed")
				}
				return "status:confirmed", nil
			}))
			args := []string{"-i", server.URL, "--broad-poc", "--thread=10", "--timeout=1"}
			if test.explicit != "" {
				args = append(args, "--verify="+test.explicit)
			}
			out, result, err := command.execute(ctx, args, io.Discard)
			if test.failure == "" && err != nil {
				t.Fatal(err)
			}
			if test.failure != "" && err == nil {
				t.Fatal("scan hid verification failure")
			}
			if test.failure == "cancel" && (!errors.Is(err, context.Canceled) || !strings.Contains(out, "canceled")) {
				t.Fatalf("cancel result: %s, %v", out, err)
			}
			if test.failure == "auth" && !strings.Contains(out, "failed") {
				t.Fatalf("failure result: %s", out)
			}
			if (calls > 0) != test.verify {
				t.Fatalf("verification calls=%d, want verification=%v", calls, test.verify)
			}
			if result == nil || len(result.Artifacts) == 0 {
				t.Fatal("local template produced no evidence")
			}
			artifacts := map[string]bool{}
			var loots []*toolpb.Loot
			for _, event := range events {
				if event.SessionId != "evidence-session" {
					t.Fatalf("lost invocation context: %v", event)
				}
				if ext := event.GetExtension(); ext != nil {
					artifact := new(toolpb.Artifact)
					if ext.MessageIs(artifact) && ext.UnmarshalTo(artifact) == nil {
						artifacts[artifact.ResultId] = true
					}
					loot := new(toolpb.Loot)
					if ext.MessageIs(loot) && ext.UnmarshalTo(loot) == nil && loot.Kind == parsers.LootVuln {
						loots = append(loots, loot)
					}
				}
			}
			if len(loots) == 0 {
				t.Fatal("vulnerability evidence was not published")
			}
			for _, loot := range loots {
				if !artifacts[loot.ResultId] {
					t.Fatalf("loot has no archived original: %v", loot)
				}
				want := ""
				if test.verify {
					want = "confirmed"
				}
				if test.failure != "" {
					want = "inconclusive"
				}
				if loot.VerificationStatus != want {
					t.Fatalf("verification=%q, want %q", loot.VerificationStatus, want)
				}
			}
		})
	}
}

func TestVerificationRejectsMissingModelAndRetiredValuesBeforeInput(t *testing.T) {
	for _, value := range []string{"", "auto", "low", "medium", "high", "critical", "on"} {
		command := New(nil)
		_, _, err := command.execute(t.Context(), []string{"--verify=" + value}, io.Discard)
		if err == nil || strings.Contains(err.Error(), "no input") {
			t.Fatalf("%s reached input processing: %v", value, err)
		}
	}
	for _, value := range []string{"off"} {
		_, _, err := New(nil).execute(t.Context(), []string{"--verify=" + value}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "no input") {
			t.Fatalf("%q: %v", value, err)
		}
	}
}
