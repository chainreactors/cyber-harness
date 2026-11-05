import type { GraphExtension } from '@cyber/ioa'
import type { IoaMessageRecord } from '@cyber/ioa'

export type HarnessEvent = 'message' | 'inbox.route' | 'subagent.delegate' | 'subagent.return' | 'task.result'
export interface HarnessAddress { node_id?: string; task_id?: string; session_id?: string }
export interface HarnessEnvelope {
  version: 1
  event: HarnessEvent
  id?: string
  source?: HarnessAddress
  target?: HarnessAddress
  interrupt?: boolean
  chain?: string[]
  agent?: { name?: string; type?: string; mode?: string; model?: string }
  status?: string
  call_id?: string
}

const object = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value)
const fields = (value: Record<string, unknown>, names: string[]) => Object.keys(value).every(key => names.includes(key))
const strings = (value: Record<string, unknown>) => Object.values(value).every(item => typeof item === 'string')

// A malformed or future application envelope remains inspectable as raw IOA
// data, but must not create guessed links or crash the whole console.
export function parseHarness(meta?: Record<string, unknown>): HarnessEnvelope | undefined {
  const value = meta
  if (!object(value)) return
  if (value.version !== 1 || !['message', 'inbox.route', 'subagent.delegate', 'subagent.return', 'task.result'].includes(String(value.event))) return
  for (const key of ['id', 'status', 'call_id']) if (value[key] !== undefined && typeof value[key] !== 'string') return
  for (const key of ['source', 'target']) {
    const address = value[key]
    if (address !== undefined && (!object(address) || !fields(address, ['node_id', 'task_id', 'session_id']) || !strings(address))) return
  }
  if (value.agent !== undefined && (!object(value.agent) || !fields(value.agent, ['name', 'type', 'mode', 'model']) || !strings(value.agent))) return
  if (value.interrupt !== undefined && typeof value.interrupt !== 'boolean') return
  if (value.chain !== undefined && (!Array.isArray(value.chain) || !value.chain.every(item => typeof item === 'string'))) return
  const record = value as unknown as HarnessEnvelope
  if (record.event === 'message') {
    if (record.agent || record.status || record.call_id || record.chain?.length) return
  } else {
    if (!record.id || record.interrupt) return
  }
  if (['inbox.route', 'subagent.delegate'].includes(record.event) && !record.target?.task_id && !record.target?.session_id) return
  if (record.event === 'subagent.return' && !record.source?.session_id) return
  if (record.event === 'inbox.route' && record.call_id) return
  if (record.event === 'task.result' && (record.agent || record.call_id)) return
  if (record.event !== 'task.result' && record.chain?.length) return
  return record
}

// Task/session identities are scoped by IOA node. They express affiliation,
// never an inferred causal edge between every execution of the same task.
export function harnessGraph(messages: IoaMessageRecord[]): GraphExtension {
  const nodes = new Map<string, GraphExtension['nodes'][number]>()
  const edges = new Map<string, GraphExtension['edges'][number]>()
  const link = (source: string, target: string, label: string) => {
    edges.set(JSON.stringify([source, target, label]), { source, target, label })
  }
  const entity = (message: IoaMessageRecord, node: string, kind: 'task' | 'session', id: string) => {
    const key = 'harness:' + JSON.stringify([message.space_id ?? '', node, kind, id])
    nodes.set(key, { id: key, title: (kind === 'task' ? 'Task ' : 'Session ') + id, kind, meta: node })
    return key
  }
  for (const message of messages) {
    const record = parseHarness(message.meta)
    if (!record || !message.id || !message.sender) continue
    const content = message.content as Record<string, unknown> | undefined
    nodes.set(message.id, { id: message.id, messageId: message.id,
      title: String(content?.title ?? content?.text ?? record.event), kind: record.event,
      meta: [message.sender, record.id].filter(Boolean).join(' · ') })
    const address = (value: HarnessAddress | undefined, scope: string, direction: 'source' | 'target') => {
      if (!value) return
      const task = value.task_id ? entity(message, scope, 'task', value.task_id) : undefined
      const session = value.session_id ? entity(message, scope, 'session', value.session_id) : undefined
      if (task && session) link(task, session, 'execution')
      const endpoint = session ?? task
      if (endpoint) direction === 'source' ? link(endpoint, message.id!, 'source') : link(message.id!, endpoint, 'target')
    }
    address(record.source, record.source?.node_id || message.sender, 'source')
    // Automatic local records have no node target. An unaddressed broadcast
    // has no known destination node and must not invent one.
    const targets = record.target?.node_id ? [record.target.node_id] : message.refs?.nodes?.length ? message.refs.nodes
      : ['inbox.route', 'subagent.delegate', 'subagent.return'].includes(record.event) ? [message.sender] : []
    for (const target of targets) address(record.target, target, 'target')
  }
  return { nodes: [...nodes.values()], edges: [...edges.values()] }
}
