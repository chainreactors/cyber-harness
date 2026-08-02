import type { AgentView, SessionRecord } from '../api'

// Chat sessions and live agents use the same Web-scoped node_id.
export function agentMatchesSession(agent: AgentView, session: SessionRecord): boolean {
  return agent.hello?.nodeId === session.session?.nodeId
}

// Whether some connected agent currently backs this session (i.e. a live turn
// can be dispatched to it). False means the bound node is offline.
export function isSessionAgentOnline(session: SessionRecord, agents: AgentView[]): boolean {
  return agents.some((a) => agentMatchesSession(a, session))
}
