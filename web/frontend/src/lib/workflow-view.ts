import type { AOPEvent, ViewerTimelineItem } from '@/viewer'
import { observation } from '../../cyber-ui/packages/viewer/src/lib/observations'
import type { ExtensionResolver } from '../runtime/conversation'
import type { ToolCallEntry } from '../../cyber-ui/packages/viewer/src/types/timeline'
import { eventTime, jevEvent, runtimeEvents, type JEVCompilation, type JEVSegment, type JEVCheck, type JEVRecord, type JEVStep } from './jev-view'
import { decisionOptions, decisionText, parseJEVJSON, evaluationChoice, evaluationNumber } from './jev-decisions'
import { normalizeTokenUsage } from '../../cyber-ui/packages/viewer/src/lib/token-usage'

export type WorkflowState = 'pending' | 'completed' | 'failed' | 'interrupted'
export type WorkflowNode = {
  id: string; kind: string; label: string; literal?: boolean; actor: string; lane: string
  sessionId: string; turnId: string; timestamp: number; state: WorkflowState; background?: boolean
  item?: ViewerTimelineItem; record?: JEVRecord; related?: JEVRecord[]; step?: JEVStep; events?: AOPEvent[]
}
export type WorkflowEdge = { id: string; source: string; target: string; feedback?: boolean }
export type WorkflowTurn = { id: string; sessionId: string; turnId: string; timestamp: number; nodes: WorkflowNode[]; edges: WorkflowEdge[]; live: boolean }
const scope = (...parts: string[]) => JSON.stringify(parts)

export function workflowDecision(node: WorkflowNode) {
  const records = node.related || (node.record ? [node.record] : [])
  const request = records.find(record => record.value.payload.case === 'decisionRequest')?.value.payload
  const result = records.find(record => record.value.payload.case === 'decisionResult')?.value.payload
  return { request: request?.case === 'decisionRequest' ? request.value : undefined,
    result: result?.case === 'decisionResult' ? result.value : undefined }
}

// Summaries describe recorded inputs and answers; full evidence stays in the graph cards.
export function workflowNodeSummary(node: WorkflowNode): string {
  const payload = node.record?.value.payload
  const latest = node.related?.[node.related.length - 1]?.value.payload
  let text = ''
  if (node.kind === 'decision') {
    const result = node.related?.find(record => record.value.payload.case === 'decisionResult')?.value.payload
    const answers = result?.case === 'decisionResult' ? result.value : payload?.case === 'decisionResult' ? payload.value : undefined
    const questions = payload?.case === 'decisionRequest' ? payload.value.claims : {}
    text = answers?.error || [...new Set([...Object.keys(questions), ...Object.keys(answers?.evaluations || {})])].map(id => {
      const question = questions[id], answer = answers?.evaluations[id]
      const prompt = question ? decisionText(question.context) : ''
      const choice = evaluationChoice(answer), number = evaluationNumber(answer)
      const selected = choice ? question ? decisionOptions(question, answer).find(option => option.selected)?.description || choice : choice
        : number !== undefined ? answer?.value.case === 'noul' ? `${(number * 100).toFixed(1)}%` : number.toFixed(2) : ''
      return [prompt, selected].filter(Boolean).join(' → ')
    }).filter(Boolean).join(' · ')
  } else if (node.kind === 'tool') {
    const args = node.item?.kind === 'tool_call' ? node.item.toolCall.toolArgs
      : node.step?.call?.arguments?.data || (payload?.case === 'dispatch' ? payload.value.call?.arguments?.data : undefined)
    const decoded = args instanceof Uint8Array ? new TextDecoder().decode(args) : args || ''
    const value = parseJEVJSON(decoded)
    const argument = value && typeof value === 'object' ? ['command', 'cmd', 'url', 'path', 'query']
      .map(key => (value as Record<string, unknown>)[key]).find(value => typeof value === 'string' && value.length > 0) : undefined
    text = typeof value === 'string' ? value : typeof argument === 'string' ? argument : decoded
  } else if (payload?.case === 'takeover') text = payload.value.definition?.when || ''
  else if (payload?.case === 'generation') text = latest?.case === 'generation' ? latest.value.error : payload.value.error
  else if (payload?.case === 'libraryChange') text = payload.value.reason || payload.value.claim?.context || payload.value.reflex?.when || ''
  else if (node.item?.kind === 'assistant_response') text = node.item.thinking || node.item.response?.content || ''
  else if (node.item?.kind === 'extension' && node.item.extensionType === 'eval') text = String(node.item.data.reason || '')
  return decisionText(text).replace(/\s+/g, ' ').trim().slice(0, 180)
}

// Use each recorded model counter once. Compiler summaries contain their child
// rounds, and a reasoning/response pair may share the same assistant message.
export function workflowNodeUsage(node: WorkflowNode, nodes: readonly WorkflowNode[]) {
  if (node.item?.kind === 'assistant_response') return normalizeTokenUsage(node.item.response?.metadata?.usage)
  const payload = [...(node.related || [])].reverse().find(record =>
    record.value.payload.case === 'decisionResult' || record.value.payload.case === 'generation' && record.value.payload.value.state === 'finished')?.value.payload
  if (payload?.case === 'decisionResult') return normalizeTokenUsage(payload.value.usage)
  if (payload?.case !== 'generation' || payload.value.kind === 'reflex_validation') return null
  const generation = payload.value
  if (generation.kind === 'reflex_llm' && generation.requestId && nodes.some(other => other.sessionId === node.sessionId
    && other.turnId === node.turnId && other.related?.some(record => record.value.payload.case === 'generation'
      && record.value.payload.value.parentRequestId === generation.requestId && record.value.payload.value.usage))) return null
  return normalizeTokenUsage(generation.usage)
}

// Every recorded step gets its own row on a shared downward time axis.
// Columns identify actors; horizontal position does not imply event order.
export function workflowPositions(nodes: WorkflowNode[], lanes: string[]) {
  return new Map(nodes.map((node, index) => [node.id, { row: index + 2, column: lanes.indexOf(node.lane) + 2 }]))
}

// Pair lifecycle events by their recorded identities, not by content or time.
// Each invocation owns its arguments, observations and result in one node.
export function workflowRecords(records: JEVRecord[], owner: JEVSegment | JEVCheck | JEVCompilation): WorkflowNode[] {
  const nodes: WorkflowNode[] = [], paired = new Map<string, WorkflowNode>()
  const closed = 'status' in owner ? owner.status !== 'running'
    : !['reviewing', 'generating', 'compiling'].includes(owner.state)
  for (const record of records) {
    const { event, value } = record, p = value.payload
    if (!p.case || p.case === 'libraryChange' && p.value.state === 'settled') continue
    const identity = p.case === 'decisionRequest' || p.case === 'decisionResult' ? p.value.requestId && `decision:${p.value.requestId}`
      : p.case === 'dispatch' ? `call:${p.value.call?.id || event.id}`
      : p.case === 'result' ? `call:${p.value.result?.callId || event.id}`
      : p.case === 'generation' ? `generation:${p.value.requestId || `${p.value.kind}:${p.value.attempt}`}` : undefined
    const key = identity ? scope(event.sessionId, event.turnId, identity) : undefined
    let node = key ? paired.get(key) : undefined
    // Older traces omit generation request IDs. A later start is a new attempt.
    if (p.case === 'generation' && p.value.state === 'started' && node?.state !== 'pending') node = undefined
    if (node) {
      node.related!.push(record)
      if (p.case === 'decisionResult') node.state = p.value.error ? 'failed' : 'completed'
      if (p.case === 'result') { node.state = p.value.result?.isError ? 'failed' : 'completed'; if (!node.step) node.step = { index: value.step, observations: [], records: [], result: p.value.result } }
      if (p.case === 'generation') node.state = p.value.error ? 'failed' : p.value.state === 'finished' ? 'completed' : 'pending'
      continue
    }
    const label = p.case === 'decisionRequest' || p.case === 'decisionResult' ? 'judgment'
      : p.case === 'dispatch' ? p.value.call?.name || 'execute' : p.case === 'result' ? p.value.result?.name || 'executionResult'
      : p.case === 'generation' ? p.value.kind === 'parameters_llm' ? 'runtimeArguments' : p.value.kind === 'compiler_round' ? 'compilerRound' : p.value.kind === 'reflex_validation' ? 'mechanismValidation' : p.value.kind === 'claim_llm' ? 'claimGeneration' : 'reflexGeneration'
      : p.case === 'libraryChange' ? `compilation.${p.value.state}`
      : p.case === 'observation' ? 'inputState' : p.case === 'takeover' ? 'takeover'
      : p.case === 'handoff' ? 'handoff' : 'check'
    const kind = p.case === 'decisionRequest' || p.case === 'decisionResult' ? 'decision'
      : p.case === 'dispatch' || p.case === 'result' ? 'tool' : p.case === 'libraryChange' ? 'publication' : p.case
    const failed = p.case === 'decisionResult' && !!p.value.error || p.case === 'result' && !!p.value.result?.isError
      || p.case === 'generation' && !!p.value.error || p.case === 'libraryChange' && ['failed', 'draft_rejected'].includes(p.value.state)
    const pending = p.case === 'decisionRequest' || p.case === 'dispatch' || p.case === 'generation' && p.value.state === 'started'
    node = { id: scope(event.sessionId, event.turnId, event.id), kind, label,
      literal: kind === 'tool' && !!(p.case === 'dispatch' ? p.value.call?.name : p.case === 'result' ? p.value.result?.name : false),
      actor: p.case === 'generation' ? 'LLM' : kind === 'tool' ? 'Executor' : 'JEV', sessionId: event.sessionId, turnId: event.turnId,
      timestamp: eventTime(event), lane: scope(event.sessionId, value.background ? 'background' : 'foreground'), background: value.background,
      state: failed ? 'failed' : pending ? 'pending' : 'completed', record, related: [record],
      step: 'steps' in owner ? owner.steps.find(step => step.call?.id === (p.case === 'dispatch' ? p.value.call?.id : p.case === 'result' ? p.value.result?.callId : undefined) && !!step.call) : undefined }
    nodes.push(node)
    if (key) paired.set(key, node)
  }
  for (const node of nodes) if (closed && node.state === 'pending') node.state = 'interrupted'
  // Failed generations already own this exact error. Publications retain their
  // own identity; successful generated drafts are inspected at publication.
  return nodes.filter(node => {
    const payload = node.record?.value.payload
    if (payload?.case !== 'libraryChange' || !payload.value.reason) return true
    return !nodes.some(other => other !== node && other.related?.some(record =>
      record.value.payload.case === 'generation' && record.value.payload.value.error === payload.value.reason))
  })
}

// Library inspectors use the same projection and layout as the conversation.
export function recordWorkflows(owner: JEVSegment | JEVCompilation | JEVCheck): WorkflowTurn[] {
  const segment = 'steps' in owner
  const item: ViewerTimelineItem = { id: owner.id, kind: 'extension', timestamp: owner.timestamp,
    extensionType: segment ? 'jev_segment' : 'state' in owner ? 'jev_compilation' : 'jev_check',
    data: segment ? { segment: owner } : 'state' in owner ? { compilation: owner } : { check: owner } }
  const live = 'status' in owner ? owner.status === 'running' : ['reviewing', 'generating', 'compiling'].includes(owner.state)
  return withWorkflows([item], owner.records.map(record => record.event)).flatMap(item =>
    item.kind === 'extension' && item.extensionType === 'workflow' ? [{ ...(item.data.workflow as WorkflowTurn), live }] : [])
}

export function withWorkflows(items: ViewerTimelineItem[], source: readonly AOPEvent[], resolveExtension: ExtensionResolver = () => undefined): ViewerTimelineItem[] {
  const events = runtimeEvents(source), turns = new Map<string, WorkflowTurn>()
  const parents = new Map(events.flatMap(event => event.payload.case === 'sessionStarted' && event.payload.value.parentToolCallId
    ? [[event.sessionId, event] as const] : []))
  const startFor = (session: string, turn: string, time: number) => events.find(event => event.sessionId === session && event.turnId === turn && event.payload.case === 'turnStarted')
    || [...events].reverse().find(event => event.sessionId === session && event.payload.case === 'turnStarted' && eventTime(event) <= time)
  function owner(session: string, turn: string, time: number): WorkflowTurn {
    let start = startFor(session, turn, time)
    const visited = new Set<string>()
    while (parents.has(session) && !visited.has(session)) {
      visited.add(session)
      const child = parents.get(session)!, parent = child.payload.case === 'sessionStarted' ? child.payload.value.parentSessionId : ''
      const callId = child.payload.case === 'sessionStarted' ? child.payload.value.parentToolCallId : ''
      const call = [...events].reverse().find(event => event.sessionId === parent && event.payload.case === 'toolCall' && event.payload.value.id === callId && eventTime(event) <= eventTime(child))
      session = parent; start = startFor(parent, call?.turnId || '', eventTime(child)); turn = start?.turnId || call?.turnId || turn
    }
    turn = start?.turnId || turn
    const id = scope(session, turn || `legacy:${time}`)
    let workflow = turns.get(id)
    if (!workflow) {
      workflow = { id, sessionId: session, turnId: turn, timestamp: start ? eventTime(start) : time, nodes: [], edges: [],
        live: !!start && !events.some(event => event.sessionId === session && (event.turnId === turn && event.payload.case === 'turnEnded' || event.payload.case === 'sessionEnded')) }
      turns.set(id, workflow)
    }
    return workflow
  }
  const attached = new Set<string>(), nativeCalls = new Set<string>(), nodeIds = new Set<string>()
  const responses: Extract<ViewerTimelineItem, { kind: 'assistant_response' }>[] = []
  const collect = (entries: ViewerTimelineItem[]) => { for (const item of entries) {
    if (item.kind === 'assistant_response') { for (const tool of item.tools) for (const event of tool.observations || []) attached.add(scope(event.sessionId, event.id)); if (item.steps) collect(item.steps); else responses.push(item) }
    if (item.kind === 'tool_call') for (const event of item.toolCall.observations || []) attached.add(scope(event.sessionId, event.id))
    if (item.kind === 'subagent_run') collect(item.items)
    if (item.kind === 'extension' && item.extensionType === 'jev_segment') {
      const segment = item.data.segment as JEVSegment
      for (const step of segment.steps) {
        if (step.call) nativeCalls.add(scope(segment.sessionId, segment.turnId, step.call.id))
        for (const event of step.observations) attached.add(scope(event.sessionId, event.id))
      }
    }
  } }
  collect(items)
  function add(node: WorkflowNode) {
    if (nodeIds.has(node.id)) return
    nodeIds.add(node.id); owner(node.sessionId, node.turnId, node.timestamp).nodes.push(node)
  }
  const retained: ViewerTimelineItem[] = []
  function consume(entries: ViewerTimelineItem[], session = '', turn = '') {
    for (const item of entries) {
      if (item.kind === 'message' && item.role === 'user') { if (!session) retained.push(item); continue }
      if (item.kind === 'divider' && item.variant !== 'warning') continue
      if (item.kind === 'extension' && item.event && attached.has(scope(item.event.sessionId, item.event.id))) continue
      if (item.kind === 'assistant_response') {
        const sid = item.sessionId || session, tid = item.turnId || turn
        if (item.steps) {
          const steps = [...item.steps], recap = item.response?.metadata?.recap
          const final = [...steps].reverse().find(step => step.kind === 'assistant_response' && !!step.response?.content.trim())
          consume(steps.map(step => step === final && step.kind === 'assistant_response' && recap
            ? { ...step, response: { content: step.response?.content || '', metadata: { ...step.response?.metadata, recap } } } : step), sid, tid)
          continue
        }
        const base = { actor: item.actorName || 'Agent', sessionId: sid, turnId: tid, timestamp: item.timestamp, lane: scope(sid, 'foreground') }
        const next = responses.find(response => response !== item && response.sessionId === sid && response.turnId === tid && response.timestamp > item.timestamp)
        const textEvents = events.filter(event => event.sessionId === sid && event.turnId === tid && event.emitter === item.actorName
          && eventTime(event) >= item.timestamp && (!next || eventTime(event) < next.timestamp)
          && (event.payload.case === 'messageDelta' || event.payload.case === 'message' && event.payload.value.role === 'assistant'))
        if (item.thinking?.trim()) add({ ...base, id: scope(sid, tid, item.id, 'thinking'), kind: 'reasoning', label: 'workflow.reasoning',
          state: item.streaming ? 'pending' : 'completed', item: { ...item, tools: [], steps: undefined,
            response: item.response?.content.trim() || item.response?.metadata?.recap ? undefined : item.response } })
        else if (!item.response?.content.trim() && item.response?.metadata?.usage) add({ ...base,
          id: scope(sid, tid, item.id, 'model'), kind: 'model', label: 'workflow.model', state: item.streaming ? 'pending' : 'completed',
          item: { ...item, tools: [], steps: undefined } })
        for (const tool of item.tools) addTool(tool, base)
        if (item.response?.content.trim() || item.response?.metadata?.recap) {
          const timestamp = textEvents.length ? eventTime(textEvents[textEvents.length - 1]) : base.timestamp
          const response = { ...item, timestamp, tools: [], thinking: undefined, steps: undefined }
          // The graph keeps a completion marker for playback. The Markdown
          // response belongs to the conversation, outside the workflow inspector.
          add({ ...base, timestamp, id: scope(sid, tid, item.id, 'response'), kind: 'response', label: 'workflow.response',
            state: item.streaming ? 'pending' : 'completed', item: response })
          retained.push(response)
        }
        continue
      }
      if (item.kind === 'extension' && ['jev_segment', 'jev_check', 'jev_compilation'].includes(item.extensionType)) {
        const value = (item.data.segment || item.data.check || item.data.compilation) as JEVSegment | JEVCheck | JEVCompilation
        for (const node of workflowRecords(value.records, value)) add(node)
        continue
      }
      if (item.kind === 'subagent_run') {
        const start = parents.get(item.sessionID || ''), sid = item.sessionID || item.id
        const tid = start?.turnId || turn
        add({ id: scope(sid, tid, 'delegation'), kind: 'agent', label: item.name, literal: true, actor: item.name, sessionId: sid, turnId: tid,
          timestamp: item.timestamp, lane: scope(sid, 'foreground'), state: item.status === 'running' || item.status === 'starting' ? 'pending' : item.status === 'failed' ? 'failed' : item.status === 'canceled' ? 'interrupted' : 'completed',
          item: { ...item, items: [] } })
        consume(item.items, sid, tid); continue
      }
      const event = item.kind === 'extension' ? item.event : undefined
      if (item.kind === 'extension' && !['guardrail', 'eval', 'compact', 'token_budget'].includes(item.extensionType)
        && !resolveExtension(item.extensionType) && !(event && observation(event))) { retained.push(item); continue }
      const nearby = event || [...events].reverse().find(candidate => !parents.has(candidate.sessionId) && eventTime(candidate) <= item.timestamp)
      const sid = event?.sessionId || session || nearby?.sessionId || 'conversation', tid = event?.turnId || turn || nearby?.turnId || ''
      const base = { actor: item.actorName || 'Agent', sessionId: sid, turnId: tid, timestamp: item.timestamp, lane: scope(sid, 'foreground') }
      if (item.kind === 'tool_call') { addTool(item.toolCall, base); continue }
      const label = item.kind === 'extension' ? item.extensionType === 'guardrail' ? 'workflow.guardrail' : item.extensionType === 'eval' ? 'workflow.evaluation' : item.extensionType
        : item.kind === 'divider' ? 'workflow.error' : item.role === 'thinking' ? 'workflow.reasoning' : 'workflow.response'
      add({ ...base, id: scope(sid, tid, item.id), kind: item.kind === 'extension' ? item.extensionType : item.kind,
        label, literal: item.kind === 'extension' && !['guardrail', 'eval'].includes(item.extensionType), state: item.kind === 'divider' || item.kind === 'extension' && item.extensionType === 'eval' && item.data.pass === false ? 'failed'
          : item.kind === 'extension' && item.data.awaiting === true ? 'pending' : 'completed', item })
    }
  }
  function addTool(tool: ToolCallEntry, base: Pick<WorkflowNode, 'actor' | 'sessionId' | 'turnId' | 'timestamp' | 'lane'>) {
    if (nativeCalls.has(scope(base.sessionId, base.turnId, tool.id))) return
    const call = events.find(event => event.sessionId === base.sessionId && event.turnId === base.turnId && event.payload.case === 'toolCall' && event.payload.value.id === tool.id && !jevEvent(event))
    add({ ...base, timestamp: call ? eventTime(call) : base.timestamp, id: scope(base.sessionId, base.turnId, 'tool', tool.id),
      kind: 'tool', label: tool.toolName, literal: true, state: tool.pending ? 'pending' : tool.error ? 'failed' : 'completed',
      events: events.filter(event => event.sessionId === base.sessionId && event.turnId === base.turnId
        && (event.payload.case === 'toolCall' && event.payload.value.id === tool.id || event.payload.case === 'toolResult' && event.payload.value.callId === tool.id)),
      item: { id: tool.id, kind: 'tool_call', timestamp: base.timestamp, toolCall: tool } })
  }
  consume(items)
  for (const workflow of turns.values()) {
    workflow.nodes.sort((a, b) => a.timestamp - b.timestamp)
    workflow.timestamp = workflow.nodes[0]?.timestamp ?? workflow.timestamp
    for (const node of workflow.nodes) {
      node.lane = scope(node.sessionId, node.background ? 'background' : node.actor === 'JEV' ? 'JEV' : node.kind === 'tool' ? 'Executor' : 'Agent')
      if (!node.background && node.state === 'pending' && events.some(event => event.sessionId === node.sessionId
        && (event.turnId === node.turnId && event.payload.case === 'turnEnded' || event.payload.case === 'sessionEnded'))) node.state = 'interrupted'
    }
    const last = new Map<string, WorkflowNode>()
    for (const node of workflow.nodes) {
      const stream = scope(node.sessionId, node.background ? 'background' : 'foreground')
      const previous = last.get(stream)
      if (previous) workflow.edges.push({ id: scope(previous.id, node.id), source: previous.id, target: node.id, feedback: previous.kind === 'tool' && node.kind === 'observation' })
      else if (node.background || node.sessionId !== workflow.sessionId) {
        const parent = parents.get(node.sessionId)
        const parentId = parent?.payload.case === 'sessionStarted' ? parent.payload.value.parentSessionId : workflow.sessionId
        const parentCall = parent?.payload.case === 'sessionStarted' ? parent.payload.value.parentToolCallId : undefined
        const from = [...workflow.nodes].reverse().find(candidate => candidate.sessionId === parentId && !candidate.background
          && candidate.timestamp <= node.timestamp && (!parentCall || candidate.item?.kind === 'tool_call' && candidate.item.toolCall.id === parentCall))
        if (from) workflow.edges.push({ id: scope(from.id, node.id), source: from.id, target: node.id })
      }
      last.set(stream, node)
    }
    // Join delegated work only when its actual session-ended evidence exists.
    for (const [lane, childLast] of last) if (childLast.sessionId !== workflow.sessionId && !childLast.background) {
      const end = events.find(event => event.sessionId === childLast.sessionId && event.payload.case === 'sessionEnded')
      const next = end && workflow.nodes.find(node => node.sessionId === workflow.sessionId && !node.background && node.timestamp >= eventTime(end))
      if (next && lane !== next.lane) workflow.edges.push({ id: scope(childLast.id, next.id), source: childLast.id, target: next.id, feedback: true })
    }
    retained.push({ id: `workflow:${workflow.id}`, kind: 'extension', extensionType: 'workflow', timestamp: workflow.timestamp,
      actorName: 'Agent', data: { workflow } })
  }
  return retained.sort((a, b) => a.timestamp - b.timestamp || (a.kind === 'message' && a.role === 'user' ? -1 : 1))
}
