package session

import (
	"context"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/core/types"
)

func TestDirectCommandInheritsCurrentAgentConfig(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	var observed agent.Config
	var found bool
	var err error
	rt.commands, rt.commandIndex, err = commandDeclarations([]Command{{
		Spec: &types.CommandSpec{Name: "/inspect-config"},
		Handler: func(ctx context.Context, _ *Session, _ []string) (*types.CommandResult, error) {
			observed, found = agent.ToolAgentConfig(ctx)
			return nil, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := rt.OpenSession(t.Context(), SessionOptions{ID: "selected-session"})
	if err != nil {
		t.Fatal(err)
	}
	p := &scriptedProvider{}
	s.state.agent.SetProvider(p, "selected-model")
	if _, err := s.Command(t.Context(), "/inspect-config"); err != nil {
		t.Fatal(err)
	}
	if !found || observed.Model != "selected-model" || observed.Provider != p || observed.SessionID != s.ID() {
		t.Fatalf("command received stale config: model=%s session=%s present=%v", observed.Model, observed.SessionID, found)
	}
}

func TestCommandDeclarationOwnsDispatchAliasesAndCatalog(t *testing.T) {
	runtime := newBareRuntime(t, nil, nil)
	spec := &types.CommandSpec{Name: "/inspect", Aliases: []string{"/peek"}, Description: "Inspect this session"}
	owner, err := newUnitResource(t, nil, Config{Commands: []Command{{Spec: spec, AdvertiseRemote: true, Handler: func(_ context.Context, s *Session, args []string) (*types.CommandResult, error) {
		return commandText("/inspect", CommandPresentationPlain, s.ID()+":"+strings.Join(args, "|")).result, nil
	}}}})
	// Use the already-loaded minimal test host; no provider or transport starts.
	if err != nil {
		t.Fatal(err)
	}
	runtime.commands, runtime.commandIndex = owner.Runtime().commands, owner.Runtime().commandIndex
	spec.Name, spec.Aliases[0] = "/mutated", "/changed"
	session, err := runtime.EnsureSession(SessionOptions{ID: "declarations"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.Command(t.Context(), `/peek "two words"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.GetContent()[0].GetText().GetText(); got != "declarations:two words" {
		t.Fatalf("handler result: %q", got)
	}
	var found bool
	for _, value := range runtime.CommandSpecs(true) {
		if value.Name == "/inspect" {
			found = true
			value.Name, value.Aliases[0] = "/external", "/outside"
		}
	}
	if !found {
		t.Fatal("declared command absent from remote catalog")
	}
	if _, err := session.Command(t.Context(), "/peek"); err != nil {
		t.Fatal(err)
	}
	help, err := session.Command(t.Context(), "/help")
	if err != nil || !strings.Contains(help.GetContent()[0].GetText().GetText(), "/inspect") {
		t.Fatalf("help misses declaration: %v %v", help, err)
	}
}

func TestCommandDeclarationsRejectAmbiguousNames(t *testing.T) {
	handler := func(context.Context, *Session, []string) (*types.CommandResult, error) { return nil, nil }
	for _, commands := range [][]Command{
		{{Spec: &types.CommandSpec{Name: "/status"}, Handler: handler}},
		{{Spec: &types.CommandSpec{Name: "/custom", Aliases: []string{"/goal"}}, Handler: handler}},
		{{Spec: &types.CommandSpec{Name: "missing-slash"}, Handler: handler}},
		{{Spec: &types.CommandSpec{Name: "/custom"}}},
	} {
		if _, err := newUnitResource(t, nil, Config{Commands: commands}); err == nil {
			t.Fatalf("accepted invalid declarations: %v", commands)
		}
	}
}

func TestCommandFailureDoesNotStrandSessionQueue(t *testing.T) {
	runtime := newBareRuntime(t, nil, nil)
	var err error
	runtime.commands, runtime.commandIndex, err = commandDeclarations([]Command{{
		Spec:    &types.CommandSpec{Name: "/broken"},
		Handler: func(context.Context, *Session, []string) (*types.CommandResult, error) { panic("test handler") },
	}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := runtime.EnsureSession(SessionOptions{ID: "failure"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Command(t.Context(), "/broken"); err == nil || !strings.Contains(err.Error(), "test handler") {
		t.Fatalf("handler failure: %v", err)
	}
	if _, err := session.Command(t.Context(), "/status"); err != nil {
		t.Fatalf("queue stranded: %v", err)
	}
	if err := runtime.CloseSession(t.Context(), "failure", SessionCloseCompleted); err != nil {
		t.Fatal(err)
	}
}

func TestCommandCatalogPreservesExposureWithoutLoad(t *testing.T) {
	owner, err := newUnitResource(t, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if got := owner.Runtime().CommandSpecs(true); len(got) != 3 {
		t.Fatalf("remote command surface changed: %v", got)
	}
	for _, value := range owner.Runtime().CommandSpecs(true) {
		if value.Name == "/eval" || value.Name == "/loop" || value.Name == "/help" {
			t.Fatalf("local catalog entry exposed remotely: %s", value.Name)
		}
	}
}

// /eval rounds carries the pacing, in a number or in plain language, and the
// session hands it to the next run.
func TestEvalRoundsCommandSetsSessionPacing(t *testing.T) {
	runtime := newBareRuntime(t, nil, nil)
	owner, err := newUnitResource(t, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	runtime.commands, runtime.commandIndex = owner.Runtime().commands, owner.Runtime().commandIndex
	session, err := runtime.EnsureSession(SessionOptions{ID: "eval-rounds"})
	if err != nil {
		t.Fatal(err)
	}
	state := session.baseState().commands

	result, err := session.Command(t.Context(), "/eval rounds")
	if err != nil || !strings.Contains(result.GetContent()[0].GetText().GetText(), "auto") {
		t.Fatalf("default rounds = %v, %v, want auto", result, err)
	}
	if _, err := session.Command(t.Context(), "/eval rounds 尽量深入，最多十轮"); err != nil {
		t.Fatal(err)
	}
	if state.evalRounds != "尽量深入，最多十轮" {
		t.Fatalf("evalRounds = %q, want the plain-language spec", state.evalRounds)
	}
	// Setting the pacing must not be mistaken for setting the criteria.
	if state.evalCriteria != "" {
		t.Fatalf("evalCriteria = %q, want it untouched", state.evalCriteria)
	}
	if _, err := session.Command(t.Context(), "/eval 必须列出所有开放端口"); err != nil {
		t.Fatal(err)
	}
	status, err := session.Command(t.Context(), "/eval")
	if err != nil {
		t.Fatal(err)
	}
	if text := status.GetContent()[0].GetText().GetText(); !strings.Contains(text, "rounds: 尽量深入，最多十轮") {
		t.Fatalf("/eval status = %q, want the pacing shown alongside the criteria", text)
	}
	if _, err := session.Command(t.Context(), "/eval rounds auto"); err != nil {
		t.Fatal(err)
	}
	if state.evalRounds != "" {
		t.Fatalf("evalRounds = %q, want it cleared by auto", state.evalRounds)
	}
}
