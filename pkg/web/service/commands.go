package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	aop "github.com/chainreactors/cyber/aop"
	types "github.com/chainreactors/cyber/core/types"
)

// Stable system-message codes mirrored by the frontend i18n catalog.
const (
	SysNoRunningTask       = "no_running_task"
	SysPaused              = "paused"
	SysFileUploaded        = "file_uploaded"
	SysNoAgentsConnected   = "no_agents_connected"
	SysAgentsList          = "agents_list"
	SysAgentNotConnected   = "agent_not_connected"
	SysSessionContextReset = "session_context_reset"
	SysHelp                = "help"
)

func (s *Service) runHubCommand(sessionID, name, args string) {
	switch name {
	case "agents":
		s.handleAgentsCommand(sessionID)
	case "help":
		s.handleHelpCommand(sessionID)
	}
}

// parseCommand splits a leading "/verb args..." into its lowercased verb
// and the trimmed remainder. ok is false when content does not begin with a
// non-empty "/verb".
func parseCommand(content string) (cmd, args string, ok bool) {
	if !strings.HasPrefix(content, "/") {
		return "", "", false
	}
	rest := strings.TrimSpace(content[1:])
	if rest == "" {
		return "", "", false
	}
	if i := strings.IndexAny(rest, " \t\r\n"); i >= 0 {
		return strings.ToLower(rest[:i]), strings.TrimSpace(rest[i:]), true
	}
	return strings.ToLower(rest), "", true
}

// handleHelpCommand renders the merged "/" command catalog (hub-scope plus the
// bound agent's reported agent-scope commands) as a system message. The catalog
// travels as metadata and the frontend renders it from its own description
// strings; the body here is only the fallback for a client that does not know
// this code, so it stays language-neutral and description-free.
func (s *Service) handleHelpCommand(sessionID string) {
	menu := s.SessionMenu(sessionID)
	s.broadcastSystemMessageMetadata(sessionID, helpFallback(menu), &types.WebMessageMetadata{
		Code:     SysHelp,
		Commands: menu,
	})
}

func helpFallback(menu []*types.CommandSpec) string {
	var b strings.Builder
	b.WriteString("**Commands**\n")
	for _, c := range menu {
		syntax := c.GetUsage()
		if syntax == "" {
			syntax = c.GetName()
		}
		fmt.Fprintf(&b, "- `%s`\n", syntax)
	}
	b.WriteString("\n`!<command>` runs a shell or pseudo command directly on the agent; any other text is sent to the agent as conversation.")
	return b.String()
}

// SessionMenu is the web "/" command catalog for a session: the hub-scope
// commands plus the bound agent's reported agent-scope commands (its skills
// included). It falls back to the static agent-scope menu when no agent is
// bound, so the menu is populated even before an agent connects. This is the
// single source both SessionService/ListCommands and /help render from.
func (s *Service) SessionMenu(sessionID string) []*types.CommandSpec {
	hubSpecs := []*types.CommandSpec{
		{Name: "/help", Description: "查看命令面板"},
		{Name: "/agents", Description: "列出已连接的 agent"},
	}
	var agentSpecs []*types.CommandSpec
	if agent := s.sessionAgent(sessionID); agent != nil {
		agentSpecs = agent.commandSpecs()
	}
	if len(agentSpecs) == 0 {
		// This is the web's offline menu, not an executable terminal console.
		agentSpecs = []*types.CommandSpec{
			{Name: "/help", Description: "查看命令面板"},
			{Name: "/status", Description: "查看 Agent 的 LLM、工具、扫描器和会话健康状态"},
			{Name: "/clear", Description: "清空当前 Agent 上下文"},
			{Name: "/resume", Description: "恢复已保存会话 (/resume 选择，/resume <path|#index>)"},
			{Name: "/compact", Description: "压缩当前 Agent 上下文 (/compact [focus instructions])"},
			{Name: "/provider", Description: "查看/管理 LLM provider 配置"},
			{Name: "/model", Description: "查看/切换当前 provider 的模型"},
			{Name: "/spaces", Description: "列出所有 space"},
			{Name: "/messages", Description: "列出 space 中的起始消息"},
			{Name: "/context", Description: "查看消息线程/上下文"},
			{Name: "/nodes", Description: "列出节点（可限定 space）"},
		}
	}
	return append(hubSpecs, agentSpecs...)
}

func (s *Service) handleAgentsCommand(sessionID string) {
	if s.agents == nil || s.agents.Count() == 0 {
		s.broadcastSystemMessage(sessionID, SysNoAgentsConnected, "No agents connected.", nil)
		return
	}
	agents := s.agents.List()
	list := make([]*types.AgentListEntry, 0, len(agents))
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d agent(s) connected:\n", len(agents)))
	for _, agentView := range agents {
		hello := agentView.GetHello()
		statusView := agentView.GetStatus()
		status := "idle"
		if agentView.GetBusy() {
			status = "busy"
		}
		shortID := hello.GetNodeId()
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		sb.WriteString(fmt.Sprintf("- **%s** (%s) — %s", hello.GetName(), shortID, status))
		entry := &types.AgentListEntry{Name: hello.GetName(), NodeId: shortID, Busy: agentView.GetBusy()}
		if statusView.GetModel() != "" {
			sb.WriteString(fmt.Sprintf(" — %s/%s", statusView.GetProvider(), statusView.GetModel()))
			entry.Provider = statusView.GetProvider()
			entry.Model = statusView.GetModel()
		}
		sb.WriteString("\n")
		list = append(list, entry)
	}
	s.broadcastSystemMessageMetadata(sessionID, sb.String(), &types.WebMessageMetadata{
		Code:      SysAgentsList,
		AgentList: &types.AgentListMetadata{Agents: list},
	})
}

func (s *Service) sessionAgent(sessionID string) *remoteAgent {
	session, err := s.store.GetSession(context.Background(), sessionID)
	if err != nil || session.GetSession().GetNodeId() == "" {
		return nil
	}
	if s.agents == nil {
		return nil
	}
	return s.agents.get(session.GetSession().GetNodeId())
}

func (s *Service) StartAgentTurn(sessionID string, request *aop.RunTurnRequest) {
	workCtx, admitted := s.beginWork()
	if !admitted {
		return
	}
	defer s.work.Done()
	agent := s.sessionAgent(sessionID)
	if agent == nil {
		s.broadcastSystemMessage(sessionID, SysAgentNotConnected,
			"Agent is not connected. Reconnect the agent to continue chatting.", nil)
		return
	}

	taskID := strings.TrimSpace(request.TurnId)
	if taskID == "" {
		taskID = generateID()
	}
	request.TurnId = taskID
	request.SessionId = sessionID
	s.resetTurnTerminal(sessionID, taskID)
	s.registerSessionTask(taskID, sessionID)
	resultCh, err := s.agents.DispatchRun(agent.NodeID(), request)
	if err != nil {
		s.finishSessionTask(taskID)
		s.broadcastHubTurnEnded(sessionID, taskID, "dispatch_failed", err.Error())
		return
	}

	s.work.Add(1)
	go func() {
		defer s.work.Done()
		var res taskResult
		var ok bool
		select {
		case res, ok = <-resultCh:
		case <-workCtx.Done():
			_ = s.agents.CancelTask(agent.NodeID(), taskID, sessionID)
			s.finishSessionTask(taskID)
			s.broadcastHubTurnEnded(sessionID, taskID, "canceled", workCtx.Err().Error())
			return
		}
		s.finishSessionTask(taskID)
		if !ok {
			s.broadcastHubTurnEnded(sessionID, taskID, "agent_disconnected", "agent disconnected")
			return
		}
		if res.Err != "" {
			s.broadcastHubTurnEnded(sessionID, taskID, "agent_run_failed", res.Err)
		}
	}()
}

func (s *Service) ExecuteSessionCommand(sessionID, line string) (string, error) {
	workCtx, admitted := s.beginWork()
	if !admitted {
		return "", fmt.Errorf("web service is closing")
	}
	defer s.work.Done()
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("command line is required")
	}
	if _, err := s.store.GetSession(context.Background(), sessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
		}
		return "", err
	}
	s.PublishUserMessage(sessionID, "", &aop.Message{Role: "user", Content: []*aop.Content{aop.Text(line)}})
	if verb, args, ok := parseCommand(line); ok {
		switch verb {
		case "help", "agents":
			operationID := generateID()
			s.work.Add(1)
			go func() { defer s.work.Done(); s.runHubCommand(sessionID, verb, args) }()
			return operationID, nil
		case "clear":
			return "", fmt.Errorf("clear requires ResetSession")
		case "stop":
			return "", fmt.Errorf("stop requires CancelTurn")
		case "exit", "quit":
			return "", fmt.Errorf("exit requires CloseSession")
		case "continue", "followup":
			return "", fmt.Errorf("%s requires RunTurn", verb)
		case "scan":
			return "", fmt.Errorf("scan is not available through the chat protocol")
		}
	}
	agent := s.sessionAgent(sessionID)
	if agent == nil {
		return "", fmt.Errorf("agent is not connected")
	}
	taskID := generateID()
	s.registerSessionTask(taskID, sessionID)
	resultCh, err := s.agents.DispatchCommand(agent.NodeID(), taskID, &types.CommandRequest{SessionId: sessionID, Line: line})
	if err != nil {
		s.finishSessionTask(taskID)
		return "", err
	}
	s.work.Add(1)
	go func() {
		defer s.work.Done()
		var res taskResult
		var ok bool
		select {
		case res, ok = <-resultCh:
		case <-workCtx.Done():
			_ = s.agents.CancelTask(agent.NodeID(), taskID, sessionID)
			s.finishSessionTask(taskID)
			s.broadcastHubError(sessionID, "command_failed", workCtx.Err().Error(), nil)
			return
		}
		s.finishSessionTask(taskID)
		if !ok {
			return
		}
		if res.Err != "" {
			// The agent's error text is a raw Go string ("context canceled" and
			// friends). Code it so the client frames it in the reader's language
			// instead of rendering the string bare.
			s.broadcastHubError(sessionID, "command_failed", res.Err, map[string]any{"error": res.Err})
		}
	}()
	return taskID, nil
}
