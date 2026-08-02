import type { AgentView, SessionRecord } from '../api'

// Session.node_uri and AgentView.node_uri are the same canonical identity.
// AgentHello.agent_id is node-local registration data and is display-only.
export function agentMatchesSession(agent: AgentView, session: SessionRecord): boolean {
  return agent.nodeUri === session.session?.nodeUri
}

// Whether some connected agent currently backs this session (i.e. a live turn
// can be dispatched to it). False means the bound node is offline.
export function isSessionAgentOnline(session: SessionRecord, agents: AgentView[]): boolean {
  return agents.some((a) => agentMatchesSession(a, session))
}
