package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/pkg/tui"
	types "github.com/chainreactors/aiscan/pkg/types"
)

// Stable system-message codes mirrored by the frontend i18n catalog.
const (
	SysNoRunningTask     = "no_running_task"
	SysPaused            = "paused"
	SysFileUploaded      = "file_uploaded"
	SysNoAgentsConnected = "no_agents_connected"
	SysAgentsList        = "agents_list"
	SysAgentNotConnected = "agent_not_connected"
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
// bound agent's reported agent-scope commands) as a system message. Broadcast
// with an empty code so the frontend shows this dynamic, already-localized text
// verbatim instead of translating it.
func (s *Service) handleHelpCommand(sessionID string) {
	var b strings.Builder
	b.WriteString("**Commands**\n")
	for _, c := range s.SessionMenu(sessionID) {
		syntax := c.Usage
		if syntax == "" {
			syntax = c.Name
		}
		if c.Description != "" {
			fmt.Fprintf(&b, "- `%s` — %s\n", syntax, c.Description)
		} else {
			fmt.Fprintf(&b, "- `%s`\n", syntax)
		}
	}
	b.WriteString("\n`!<command>` 直接在 agent 上执行 shell/伪命令;其他文本作为对话发送给 agent。")
	s.broadcastSystemMessage(sessionID, "", b.String(), nil)
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
		// Fall back to the static agent-scope menu when no agent is bound.
		r := &tui.AgentConsole{}
		agentSpecs = tui.WebMenuSpecs(r.StaticCommands())
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
	s.registerSessionTask(taskID, sessionID, agent.NodeID())
	resultCh, err := s.agents.DispatchRun(agent.NodeID(), request)
	if err != nil {
		s.finishSessionTask(taskID)
		s.broadcastHubTurnEnded(sessionID, taskID, "dispatch_failed", err.Error())
		return
	}

	go func() {
		res, ok := <-resultCh
		canceled := s.finishSessionTask(taskID)
		if canceled {
			return
		}
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
			go s.runHubCommand(sessionID, verb, args)
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
	s.registerSessionTask(taskID, sessionID, agent.NodeID())
	resultCh, err := s.agents.DispatchCommand(agent.NodeID(), taskID, &types.CommandRequest{SessionId: sessionID, Line: line})
	if err != nil {
		s.finishSessionTask(taskID)
		return "", err
	}
	go func() {
		res, ok := <-resultCh
		canceled := s.finishSessionTask(taskID)
		if !ok || canceled {
			return
		}
		if res.Err != "" {
			s.broadcastHubError(sessionID, "", res.Err, nil)
		}
	}()
	return taskID, nil
}
