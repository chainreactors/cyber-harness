import type { AgentInfo } from '../api'
import { isCompletionTool } from './agent-tools'

// AgentActivity is a compact, render-ready view of "what is this agent doing
// right now", derived from the live stats the agent streams to the hub
// (unconditionally — so it works even for session-less swarm work).
export type AgentActivity =
  | { kind: 'tool'; tool: string; detail: string }
  | { kind: 'thinking' }
  | null

// agentActivity returns the current activity of a *busy* agent, or null when the
// agent is idle / not connected. `current_tool` is set by the agent while a tool
// is executing and cleared when it finishes, so a busy agent with no current
// tool is between tool calls (thinking / calling the model).
export function agentActivity(agent?: AgentInfo | null): AgentActivity {
  if (!agent?.busy) return null
  const tool = agent.stats?.current_tool
  if (tool && !isCompletionTool(tool)) return { kind: 'tool', tool, detail: agent.stats?.current_detail ?? '' }
  return { kind: 'thinking' }
}
