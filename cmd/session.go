package cmd

import (
	"github.com/chainreactors/aiscan/pkg/agent"
	inboxpkg "github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/app"
	taskmod "github.com/chainreactors/aiscan/pkg/agent/task"
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

	taskMgr := bashTaskManager(cfg.Application.Commands)
	if taskMgr != nil {
		taskMgr.SetObserver(func(ev taskmod.TaskEvent) {
			if ev.Kind != taskmod.EventCompletion {
				return
			}
			tail := taskMgr.PeekOrEmpty(ev.TaskID, 20)
			msg := inboxpkg.NewMessage(inboxpkg.OriginTask, "user",
				taskmod.FormatCompletion(ev.TaskInfo, ev.Killed, ev.KillCause, tail))
			msg.Meta = map[string]any{
				"task_id":   ev.TaskID,
				"task_name": ev.TaskInfo.Name,
				"exit_code": ev.TaskInfo.ExitCode,
			}
			ib.Push(msg)
		})
	}

	subAgentTool := NewSubAgentTool(SubAgentConfig{
		Base: agent.Config{
			Provider: cfg.Application.Provider,
			Tools:    cfg.Application.Commands,
			Model:    cfg.Option.Model,
			Logger:   cfg.Logger,
		},
		ParentInbox: ib,
		SkillStore:  cfg.Application.Skills,
	})
	cfg.Application.Commands.RegisterTool(subAgentTool)

	agentCfg := agent.Config{
		Provider: cfg.Application.Provider,
		Tools:    cfg.Application.Commands,
		Model:    cfg.Option.Model,
		Logger:   cfg.Logger,
		Inbox:    ib,
		KeepAlive: func() bool {
			if subAgentTool.RunningCount() > 0 {
				return true
			}
			if taskMgr == nil {
				return false
			}
			return taskMgr.RunningCount() > 0
		},
	}
	if cfg.Events != nil {
		agentCfg.Emit = cfg.Events.HandleEvent
	}

	cleanup := func() {
		if taskMgr != nil {
			taskMgr.Shutdown()
		}
	}

	return &agentSession{Config: agentCfg, cleanup: cleanup}
}

func (s *agentSession) Cleanup() {
	if s.cleanup != nil {
		s.cleanup()
	}
}
