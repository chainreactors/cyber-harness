package session

import (
	"context"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent"
	agentprompt "github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/internal/testutil/apptest"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	cfg "github.com/chainreactors/cyber/pkg/config"
	promptext "github.com/chainreactors/cyber/pkg/exts/prompt"
)

func defaultPromptResolver(t *testing.T) agentprompt.Resolver {
	t.Helper()
	var resolver agentprompt.Resolver
	hosttest.Load(t, t.Context(), promptext.New(), extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		resolver, err = extension.Use[agentprompt.Resolver](scope)
		return err
	}})
	return resolver
}

type fixedPromptResolver string

func (r fixedPromptResolver) Build(context.Context, agentprompt.Context) agentprompt.Result {
	return agentprompt.Result{Prompt: string(r)}
}

func TestResolveSystemPromptUsesConfigResolver(t *testing.T) {
	rt := &Runtime{
		agentConfig: agent.Config{PromptResolver: fixedPromptResolver("runtime")},
		logger:      telemetry.NewLoggerRef(nil),
	}
	result, err := rt.resolveSystemPrompt(t.Context(), nil)
	if err != nil || result != "runtime" {
		t.Fatalf("runtime resolver result = %q, %v", result, err)
	}

	result, err = rt.resolveSystemPrompt(t.Context(), &agent.Config{PromptResolver: fixedPromptResolver("config")})
	if err != nil || result != "config" {
		t.Fatalf("config resolver result = %q, %v", result, err)
	}
}

func TestRuntimePreloadsBaseSkillOnce(t *testing.T) {
	for _, tc := range []struct {
		name   string
		skills []string
	}{
		{name: "default"},
		{name: "explicit duplicate", skills: []string{"cyber"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			option := &cfg.Option{}
			option.Skills = tc.skills
			application := apptest.NewFixture(t, telemetry.NopLogger(), nil)
			resolver := defaultPromptResolver(t)

			applicationSet := loadTestApplication(t, application)
			defer applicationSet.Close(context.Background())
			rt, err := newUnitResource(t, application, Config{BaseSkills: []string{"cyber"}, SelectedSkills: option.Skills, Logger: telemetry.NewLoggerRef(nil), Loop: agent.StandardLoop{}, PromptResolver: resolver})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			rtSet := hosttest.Set(t, extension.Provided[*apptest.Fixture](application), rt)
			if err := rtSet.Load(t.Context()); err != nil {
				t.Fatal(err)
			}
			defer rtSet.Close(context.Background())

			systemPrompt, err := rt.Runtime().resolveSystemPrompt(t.Context(), &rt.Runtime().agentConfig)
			if err != nil {
				t.Fatal(err)
			}
			if count := strings.Count(systemPrompt, "## Skill: cyber"); count != 1 {
				t.Fatalf("base skill count = %d, want 1", count)
			}
			for _, want := range []string{
				"## User Tool Restrictions",
				"## Skill: cyber",
				"# Cyber ASM and Penetration Testing",
				"must not redirect tasks outside its scope into scanning",
				"## Tool Invocation Rules",
				"## Verification Standard",
				"## Evidence & Findings",
			} {
				if !strings.Contains(systemPrompt, want) {
					t.Fatalf("system prompt missing base skill rule %q", want)
				}
			}
			for _, unwanted := range []string{
				"## Fingerprint → POC Workflow",
				"## Asset Triage",
				"## Post-Scan Analysis",
				"map the application before focused testing",
			} {
				if strings.Contains(systemPrompt, unwanted) {
					t.Fatalf("system prompt contains SOP guidance %q", unwanted)
				}
			}
		})
	}
}
