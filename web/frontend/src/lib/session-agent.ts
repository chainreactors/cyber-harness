import type { AgentInfo, ChatSession } from '../api'

// A chat session is bound at creation to the agent it was opened under — the
// backend snapshots both the transient agent_id and the (stable) agent_name
// into the session (see CreateSession in pkg/web/service.go). The hub mints a
// fresh agent id on every WebSocket (re)connect (pkg/web/agents.go), so a node
// that flaps — a local agent restarting, a deployed node bouncing to reload
// config — comes back under a NEW id but the SAME name. Matching a session to
// its live agent by id alone would therefore strand it after any reconnect, so
// fall back to the stable name. A session whose bound node is truly gone
// matches nothing here and is treated as "offline" by the callers.
export function agentMatchesSession(agent: AgentInfo, session: ChatSession): boolean {
  if (agent.id === session.agent_id) return true
  return !!session.agent_name && agent.name === session.agent_name
}

// Whether some connected agent currently backs this session (i.e. a live turn
// can be dispatched to it). False means the bound node is offline.
export function isSessionAgentOnline(session: ChatSession, agents: AgentInfo[]): boolean {
  return agents.some((a) => agentMatchesSession(a, session))
}
