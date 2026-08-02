import type { AgentView } from '../api'

// AgentActivity is a compact, render-ready view of "what is this agent doing
// right now", derived from the live stats the agent streams to the hub
// (unconditionally — so it works even for session-less swarm work).
export type AgentActivity =
  | { kind: 'tool'; tool: string; detail: string }
  | { kind: 'thinking' }
  | null

// agentActivity returns the current activity of a *busy* agent, or null when
// the agent is idle / not connected. The proto AgentStats no longer carries
// the in-flight tool fields, so a busy agent simply reads as "thinking" —
// between tool calls or inside one, the turn is running either way.
export function agentActivity(agent?: AgentView | null): AgentActivity {
  if (!agent?.busy) return null
  return { kind: 'thinking' }
}
