package cmd

import (
	"github.com/chainreactors/aiscan/pkg/agent"
	inboxpkg "github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/app"
	tmuxpkg "github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

type agentSession struct {
	Config  agent.Config
	cleanup func()
}

type sessionConfig struct {
	Application *app.App
	Option      *Option
	Logger      telemetry.Logger
	Events      *eventsWriter
}

func newAgentSession(cfg sessionConfig) *agentSession {
	ib := inboxpkg.NewBuffered(64)

	sessMgr := bashSessionManager(cfg.Application.Commands)
	if sessMgr != nil {
		sessMgr.SetOnDone(func(info tmuxpkg.Info) {
			tail := sessMgr.PeekOrEmpty(info.ID, 20)
			msg := inboxpkg.NewMessage(inboxpkg.OriginSession, "user",
				tmuxpkg.FormatCompletion(info, tail))
			msg.Meta = map[string]any{
				"session_id":   info.ID,
				"session_name": info.Name,
				"exit_code":    info.ExitCode,
			}
			if err := ib.Push(msg); err != nil {
				cfg.Logger.Warnf("inbox push session completion: %s", err)
			}
		})
	}

	scheduler := agent.NewLoopScheduler(ib, cfg.Logger)

	agentCfg := agent.Config{
		Provider:       cfg.Application.Provider,
		Tools:          cfg.Application.Commands,
		Model:          cfg.Option.Model,
		Logger:         cfg.Logger,
		Inbox:          ib,
		LoopScheduler:  scheduler,
		CacheRetention: provider.CacheShort,
	}

	cfg.Application.Commands.RegisterTool(agent.NewLoopTool(scheduler))

	subAgentTool := NewSubAgentTool(SubAgentConfig{
		ParentConfig: agentCfg,
		ParentInbox:  ib,
		SkillStore:   cfg.Application.Skills,
	})
	cfg.Application.Commands.RegisterTool(subAgentTool)

	if cfg.Events != nil {
		agentCfg.Emit = cfg.Events.HandleEvent
	}

	cleanup := func() {
		scheduler.Stop()
		if sessMgr != nil {
			sessMgr.Shutdown()
		}
	}

	return &agentSession{Config: agentCfg, cleanup: cleanup}
}

func (s *agentSession) Cleanup() {
	if s.cleanup != nil {
		s.cleanup()
	}
}
