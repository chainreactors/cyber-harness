import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { usePolling } from './usePolling'
import {
  cancelChatSession,
  createChatSession,
  deleteChatSession,
  getChatSession,
  listAgents,
  listChatMessages,
  listChatSessions,
  sendChatMessage,
  subscribeChatEvents,
  getScan,
} from '../api'
import type { AgentInfo, ChatEvent, ChatMessage, ChatSession, ScanResult } from '../api'
import {
  isRootPath,
  parseRoute,
  setSessionRoute,
  type RouteMode,
} from '../lib/scan-route'

// safeUUID() only exists in secure contexts (HTTPS or localhost).
// When the UI is served over plain HTTP on a LAN/public IP it is undefined,
// which would throw when sending a message or rendering events. Fall back to
// crypto.getRandomValues (available in insecure contexts) and finally Math.random.
function safeUUID(): string {
  const c: Crypto | undefined = typeof crypto !== 'undefined' ? crypto : undefined
  if (c && typeof c.randomUUID === 'function') {
    try {
      return c.randomUUID()
    } catch {
      // fall through to the manual generators below
    }
  }
  if (c && typeof c.getRandomValues === 'function') {
    const b = c.getRandomValues(new Uint8Array(16))
    b[6] = (b[6] & 0x0f) | 0x40
    b[8] = (b[8] & 0x3f) | 0x80
    const h = Array.from(b, (x) => x.toString(16).padStart(2, '0'))
    return `${h.slice(0, 4).join('')}-${h.slice(4, 6).join('')}-${h.slice(6, 8).join('')}-${h.slice(8, 10).join('')}-${h.slice(10, 16).join('')}`
  }
  return `id-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

// Build a ChatMessage from a ChatEvent, sharing the common field mapping
// (agent_id, agent_name, session_id, timestamps, i18n metadata) across all
// event→message construction sites.  `contentOverride` lets callers supply a
// value that differs from `ev.content` (e.g. an accumulated streaming buffer).
function eventToMessage(ev: ChatEvent, fallbackId: string, contentOverride?: string): ChatMessage {
  return {
    id: ev.message_id || fallbackId,
    session_id: ev.session_id,
    role: ev.role || 'assistant',
    content: contentOverride ?? ev.content ?? '',
    agent_id: ev.agent_id,
    agent_name: ev.agent_name,
    created_at: new Date().toISOString(),
    metadata: ev.code ? { code: ev.code, params: ev.params } : undefined,
  }
}

export type TimelineItemKind = 'message' | 'assistant_response' | 'tool_call' | 'scan_started' | 'scan_progress' | 'scan_complete' | 'thinking' | 'agent_joined' | 'eval'

export interface TimelineItem {
  id: string
  kind: TimelineItemKind
  timestamp: number
  message?: ChatMessage
  assistantResponse?: AssistantResponseState
  toolCall?: ToolCallState
  scanID?: string
  scanResult?: ScanResult
  scanLines?: string[]
  agentName?: string
  content?: string
  evalRound?: number
  evalPass?: boolean
  evalReason?: string
}

export interface AssistantResponseState {
  id: string
  turn?: number
  agentName?: string
  thinking?: string
  tools: ToolCallState[]
  response?: ChatMessage
  streaming: boolean
}

export interface ToolCallState {
  id: string
  toolName: string
  toolArgs: string
  result?: string
  pending: boolean
}

// A per-session snapshot of the durable conversation state — everything the
// panel renders that survives a switch away and back. Cached in memory so a
// revisit repaints instantly instead of flashing blank for a network fetch.
interface SessionSnapshot {
  messages: ChatMessage[]
  timeline: TimelineItem[]
  scanResults: Map<string, ScanResult>
}

// A node's stable identity across reconnects. The hub mints a fresh transient
// agent id on every WebSocket (re)connect (pkg/web/agents.go), so a node that
// flaps — a local agent restarting, a deployed node bouncing to reload config —
// briefly leaves the roster and returns under a new id. Key selection on the
// node_name (falling back to name, then id) so it follows the same logical node
// instead of being reset to an unrelated one.
export function agentNodeKey(a: AgentInfo): string {
  return a.identity?.node_name || a.name || a.id
}

// Deterministic roster order. The hub returns agents in Go-map iteration order,
// which is randomized per request; without a stable sort the sidebar reshuffles
// on every 5s poll. Ordering by node key keeps the list — and any "first agent"
// auto-pick — put across refreshes.
function sortAgentsByNode(list: AgentInfo[]): AgentInfo[] {
  return [...list].sort((a, b) => agentNodeKey(a).localeCompare(agentNodeKey(b)))
}

// Cheap staleness probe for the cache revalidation fast-path. Persisted history
// is append-only within a run, so a differing length or a changed last-message
// id/content is enough to know the cached snapshot no longer matches the server
// — which lets a revisit skip the setState + full timeline rebuild whenever
// nothing actually changed while it was away.
function messagesDiffer(a: ChatMessage[], b: ChatMessage[]): boolean {
  if (a.length !== b.length) return true
  if (a.length === 0) return false
  const la = a[a.length - 1]
  const lb = b[b.length - 1]
  return la.id !== lb.id || la.content !== lb.content
}

export function useChatSession() {
  const { t } = useTranslation('chat')
  const [agents, setAgents] = useState<AgentInfo[]>([])
  const [selectedAgentID, setSelectedAgentID] = useState<string | null>(null)
  const [sessions, setSessions] = useState<ChatSession[]>([])
  const [activeSessionID, setActiveSessionID] = useState<string | null>(null)
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [timeline, setTimeline] = useState<TimelineItem[]>([])
  const timelineRef = useRef<TimelineItem[]>([])
  const [scanResults, setScanResults] = useState<Map<string, ScanResult>>(() => new Map())
  const [isThinking, setIsThinking] = useState(false)
  const [pendingResponse, setPendingResponse] = useState(false)
  const [error, setError] = useState('')
  const unsubRef = useRef<(() => void) | null>(null)
  const activationRef = useRef(0)
  const userMsgIdsRef = useRef<Set<string>>(new Set())
  const scanLinesRef = useRef<Map<string, string[]>>(new Map())
  const activeTurnRef = useRef<string | null>(null)
  // Bumped on every send. The backend restarts its turn counter at 1 for each
  // user message, so without a per-run discriminator the new answer's id would
  // collide with the previous run's turn-1 bubble and render above the message.
  const runEpochRef = useRef(0)
  const activeSessionRef = useRef<string | null>(null)
  // Latest roster (mirrors `agents`) so click handlers can resolve an id → node
  // key without waiting for a re-render, and the stable key of the node the user
  // last chose. Selection is tracked by this key, not the transient id, so the
  // 5s agent poll can re-home it across reconnects instead of snapping to list[0].
  const agentsRef = useRef<AgentInfo[]>([])
  const selectedNodeKeyRef = useRef<string | null>(null)
  const sessionCacheRef = useRef<Map<string, SessionSnapshot>>(new Map())

  useEffect(() => {
    activeSessionRef.current = activeSessionID
  }, [activeSessionID])

  // Mirror the active session's durable state into an in-memory cache keyed by
  // session id. activateSession repaints this snapshot synchronously on
  // re-entry, so switching back to a session jumps straight to its conversation
  // instead of blanking for a round-trip. Writing on every durable change (vs.
  // snapshotting on leave) keeps the cache live with streamed SSE updates
  // without threading cache writes through every setMessages call site — and
  // because each render's id and messages are captured together, a switch can
  // never file the incoming session's state under the outgoing session's key.
  useEffect(() => {
    if (!activeSessionID) return
    sessionCacheRef.current.set(activeSessionID, { messages, timeline, scanResults })
  }, [activeSessionID, messages, timeline, scanResults])

  const refreshAgents = useCallback(async () => {
    try {
      const list = sortAgentsByNode(await listAgents())
      agentsRef.current = list
      setAgents(list)
      setSelectedAgentID((current) => {
        // Follow the user's chosen node by its stable key rather than its
        // transient id: a reconnect changes the id but not the key, so the
        // selection re-homes onto the same node instead of jumping to whichever
        // node happens to sort first this poll.
        const key = selectedNodeKeyRef.current
        if (key) {
          const match = list.find((a) => agentNodeKey(a) === key)
          if (match) return match.id
          // Node is momentarily absent (mid-reconnect) — keep the selection put;
          // it re-homes above once the node comes back. Don't yank it elsewhere.
          return current
        }
        // Nothing chosen yet → auto-select the first node and remember it.
        const first = list[0]
        if (first) selectedNodeKeyRef.current = agentNodeKey(first)
        return first?.id || null
      })
    } catch {}
  }, [])

  const refreshSessions = useCallback(async () => {
    try {
      setSessions(await listChatSessions())
    } catch {}
  }, [])

  useEffect(() => {
    refreshAgents()
    refreshSessions()
  }, [refreshAgents, refreshSessions])
  // Roster poll — paused while the tab is hidden (this runs for the whole app
  // lifetime, so a backgrounded tab would otherwise keep hitting /api/agents
  // every 5s forever).
  usePolling(refreshAgents, 5000)

  function closeSubscription() {
    if (unsubRef.current) {
      unsubRef.current()
      unsubRef.current = null
    }
  }

  // Wipe the transient per-run state (streaming buffers, thinking/pending flags,
  // turn/epoch bookkeeping) while leaving the durable conversation untouched.
  // Both a cold open and a cache restore want this cleared — only their handling
  // of the durable state (messages/timeline/scans) differs.
  function resetTransientState() {
    setIsThinking(false)
    setPendingResponse(false)
    setError('')
    userMsgIdsRef.current = new Set()
    scanLinesRef.current = new Map()
    activeTurnRef.current = null
    runEpochRef.current = 0
  }

  function resetSessionState() {
    setMessages([])
    timelineRef.current = []
    setTimeline([])
    setScanResults(new Map())
    resetTransientState()
  }

  // Repaint a cached session's durable state instantly (see sessionCacheRef).
  // Runs the same transient wipe as a cold open so a half-streamed response or
  // stale thinking dots from the previous session can't bleed across the switch.
  function restoreSnapshot(snap: SessionSnapshot) {
    setMessages(snap.messages)
    timelineRef.current = snap.timeline
    setTimeline(snap.timeline)
    setScanResults(snap.scanResults)
    resetTransientState()
  }

  function appendTimeline(item: TimelineItem) {
    setTimelineItems((prev) => [...prev, item])
  }

  function appendMessage(msg: ChatMessage, timestamp: number) {
    setMessages((prev) => {
      const last = prev[prev.length - 1]
      if (isDuplicateAssistantMessage(last, msg)) return prev
      return [...prev, msg]
    })
    setTimelineItems((prev) => {
      const last = prev[prev.length - 1]
      if (isDuplicateAssistantTimelineItem(last, msg, timestamp)) return prev
      return [...prev, { id: msg.id, kind: 'message', timestamp, message: msg }]
    })
  }

  function setTimelineItems(updater: (prev: TimelineItem[]) => TimelineItem[]) {
    setTimeline((prev) => {
      const next = updater(prev)
      timelineRef.current = next
      return next
    })
  }

  function updateTimelineItem(id: string, updater: (item: TimelineItem) => TimelineItem) {
    setTimelineItems((prev) => prev.map((item) => item.id === id ? updater(item) : item))
  }

  function isDuplicateAssistantResponse(content: string) {
    const items = timelineRef.current
    for (let i = items.length - 1; i >= 0; i--) {
      const item = items[i]
      if (item.kind === 'message' && item.message?.role === 'user') return false
      if (item.kind === 'assistant_response') {
        return isDuplicateAssistantResponseContent(item, content)
      }
    }
    return false
  }

  function assistantResponseID(timestamp = Date.now(), event?: ChatEvent) {
    if (event?.turn) {
      if (!event.agent_id && activeTurnRef.current?.endsWith(`-${event.turn}`)) {
        return activeTurnRef.current
      }
      const scope = [runEpochRef.current, event.session_id || activeSessionID || 'session', event.agent_id || event.agent_name || 'agent', event.turn].join('-')
      const id = `turn-${scope}`
      activeTurnRef.current = id
      return id
    }
    if (activeTurnRef.current) return activeTurnRef.current
    const id = `turn-${timestamp}-${safeUUID()}`
    activeTurnRef.current = id
    return id
  }

  function upsertAssistantResponse(
    updater: (response: AssistantResponseState) => AssistantResponseState,
    event?: ChatEvent,
    timestamp = Date.now(),
    responseID?: string,
  ) {
    const id = responseID || assistantResponseID(timestamp, event)
    const agentName = event?.agent_name
    setTimelineItems((prev) => {
      const index = prev.findIndex((item) => item.id === id)
      if (index >= 0) {
        const item = prev[index]
        const current = item.assistantResponse || {
          id,
          turn: event?.turn,
          agentName: agentName || item.agentName,
          tools: [],
          streaming: false,
        }
        const next = prev.slice()
        const response = updater({
          ...current,
          turn: event?.turn || current.turn,
          agentName: agentName || current.agentName,
        })
        next[index] = {
          ...item,
          kind: 'assistant_response',
          agentName: response.agentName,
          assistantResponse: response,
        }
        return next
      }
      const response = updater({
        id,
        turn: event?.turn,
        agentName,
        tools: [],
        streaming: false,
      })
      return [
        ...prev,
        {
          id,
          kind: 'assistant_response',
          timestamp,
          agentName: response.agentName,
          assistantResponse: response,
        },
      ]
    })
  }

  function setAssistantThinking(event: ChatEvent, content: string, timestamp: number) {
    upsertAssistantResponse((response) => ({
      ...response,
      thinking: content || response.thinking,
      streaming: true,
    }), event, timestamp)
  }

  function setAssistantResponseMessage(event: ChatEvent, content: string, streaming: boolean, timestamp: number) {
    const responseID = assistantResponseID(timestamp, event)
    const msg = eventToMessage(event, `assistant-response-${responseID}`, content)
    upsertAssistantResponse((response) => ({
      ...response,
      agentName: event.agent_name || response.agentName,
      response: msg,
      streaming,
    }), event, timestamp, responseID)
  }

  function upsertAssistantTool(event: ChatEvent, toolCall: ToolCallState, timestamp: number) {
    upsertAssistantResponse((response) => {
      const index = response.tools.findIndex((item) => item.id === toolCall.id)
      const tools = response.tools.slice()
      if (index >= 0) {
        tools[index] = { ...tools[index], ...toolCall }
      } else {
        tools.push(toolCall)
      }
      return { ...response, tools, streaming: true }
    }, event, timestamp)
  }

  function updateAssistantToolResult(event: ChatEvent, toolCallID: string, result: string | undefined) {
    const id = assistantResponseID(Date.now(), event)
    updateTimelineItem(id, (item) => {
      if (!item.assistantResponse) return item
      return {
        ...item,
        assistantResponse: {
          ...item.assistantResponse,
          tools: item.assistantResponse.tools.map((tool) => (
            tool.id === toolCallID ? { ...tool, result, pending: false } : tool
          )),
        },
      }
    })
  }

  // Release the composer and stop every streaming indicator. Called at the two
  // points a run stops producing output: the hub's terminal aggregate message
  // (normal completion, including eval max-rounds) and an explicit cancel.
  // A tool-only turn emits message_start (streaming=true) but the hub drops its
  // empty-content message_end, so its assistant_response never flips back to
  // done. Since `busy` ORs over every response's streaming flag, one orphaned
  // flag keeps the send/stop button stuck on "stop" after the run has ended —
  // most visible on long multi-turn / eval runs. Sweep them all here.
  function finalizeRun() {
    setIsThinking(false)
    setPendingResponse(false)
    setTimelineItems((prev) => {
      let changed = false
      const next = prev.map((item) => {
        if (item.kind === 'assistant_response' && item.assistantResponse?.streaming) {
          changed = true
          return { ...item, assistantResponse: { ...item.assistantResponse, streaming: false } }
        }
        return item
      })
      return changed ? next : prev
    })
  }

  function handleChatEvent(event: ChatEvent) {
    const now = Date.now()

    switch (event.type) {
      case 'message': {
        const isUserEcho = event.message_id && userMsgIdsRef.current.has(event.message_id)
        if (isUserEcho) break
        if ((event.role || 'assistant') === 'assistant') {
          // The hub persists the run's aggregate reply as this event once the
          // agent finishes — the authoritative "run is over" signal. Record the
          // text (unless it merely echoes the already-streamed answer), then
          // finalize the run even when the content is empty, so a run that ended
          // on tool calls or hit its eval round cap still releases the composer.
          if (event.content && !(!activeTurnRef.current && isDuplicateAssistantResponse(event.content))) {
            setAssistantResponseMessage(event, event.content, false, now)
          }
          finalizeRun()
          break
        }
        if (!event.content) break
        const msg = eventToMessage(event, safeUUID())
        appendMessage(msg, now)
        setIsThinking(false)
        setPendingResponse(false)
        break
      }

      case 'message_start':
        setAssistantResponseMessage(event, event.content || '', true, now)
        setIsThinking(false)
        break

      case 'message_delta':
        if (event.content !== undefined) {
          setAssistantResponseMessage(event, event.content, true, now)
        } else if (event.delta) {
          upsertAssistantResponse((response) => {
            const prevContent = response.response?.content || ''
            const msg: ChatMessage = response.response || eventToMessage(event, `assistant-response-${response.id}`, '')
            return {
              ...response,
              response: { ...msg, content: prevContent + event.delta },
              streaming: true,
            }
          }, event, now)
        }
        break

      case 'message_end': {
        const finalContent = event.content || ''
        if (finalContent) {
          setAssistantResponseMessage(event, finalContent, false, now)
        } else {
          upsertAssistantResponse((response) => ({ ...response, streaming: false }), event, now)
        }
        setIsThinking(false)
        setPendingResponse(false)
        break
      }

      case 'tool_call': {
        const tcID = event.tool_call_id || safeUUID()
        const tc: ToolCallState = {
          id: tcID,
          toolName: event.tool_name || '',
          toolArgs: event.tool_args || '',
          pending: true,
        }
        upsertAssistantTool(event, tc, now)
        setIsThinking(false)
        break
      }

      case 'tool_result': {
        if (!event.tool_call_id) break
        const tcID = event.tool_call_id
        updateAssistantToolResult(event, tcID, event.content)
        break
      }

      case 'thinking':
        setIsThinking(true)
        if (event.content || event.data || event.delta) {
          setAssistantThinking(event, event.content || event.data || event.delta || '', now)
        } else {
          upsertAssistantResponse((response) => ({ ...response, streaming: true }), event, now)
        }
        break

      case 'session_cleared':
        // Web /clear wiped this session's transcript server-side; mirror it in the
        // UI. resetSessionState() empties messages+timeline — and since the timeline
        // is a projection of messages, a page reload re-derives an empty view too.
        resetSessionState()
        break

      case 'scan_started':
        if (event.scan_id) {
          scanLinesRef.current.set(event.scan_id, [])
          appendTimeline({
            id: `scan-${event.scan_id}`,
            kind: 'scan_started',
            timestamp: now,
            scanID: event.scan_id,
            scanLines: [],
            content: event.data,
          })
        }
        break

      case 'scan_progress':
        if (event.scan_id && event.data) {
          const lines = scanLinesRef.current.get(event.scan_id) || []
          lines.push(event.data)
          scanLinesRef.current.set(event.scan_id, lines)
          updateTimelineItem(`scan-${event.scan_id}`, (item) => ({
            ...item,
            scanLines: [...lines],
          }))
        }
        break

      case 'scan_complete':
        if (event.scan_id && event.result) {
          setScanResults((prev) => {
            const next = new Map(prev)
            next.set(event.scan_id!, event.result!)
            return next
          })
          appendTimeline({
            id: `scanres-${event.scan_id}`,
            kind: 'scan_complete',
            timestamp: now,
            scanID: event.scan_id,
            scanResult: event.result,
          })
        }
        setPendingResponse(false)
        break

      case 'scan_error':
        if (event.error) setError(event.error)
        setPendingResponse(false)
        break

      case 'agent_joined':
        break

      case 'eval':
        appendTimeline({
          id: `eval-${event.eval_round ?? 0}-${now}`,
          kind: 'eval',
          timestamp: now,
          evalRound: event.eval_round,
          evalPass: event.eval_pass,
          evalReason: event.eval_reason,
        })
        // Round boundary — mirror buildTimelineFromMessages. A non-passing verdict
        // is followed by a re-driven round whose turn counter restarts at 1; bump
        // the run epoch so its turn-1 opens a fresh bubble instead of upserting
        // over this round's turn-1. (A pass ends the run, so the terminal
        // aggregate still merges into the last round's bubble.)
        if (!event.eval_pass) {
          runEpochRef.current += 1
          activeTurnRef.current = null
        }
        break

      case 'error':
        if (event.code) setError(t(`sys.${event.code}`, { ...(event.params || {}), defaultValue: event.error || '' }))
        else if (event.error) setError(event.error)
        finalizeRun()
        break
    }
  }

  function buildTimelineFromMessages(msgs: ChatMessage[]): TimelineItem[] {
    const built: TimelineItem[] = []
    const responsesByTurn = new Map<string, TimelineItem>()
    const toolsByID = new Map<string, ToolCallState>()
    let currentResponse: TimelineItem | null = null
    let pendingAgentName: string | undefined
    // Reload mirror of runEpochRef: each user message opens a new run whose turn
    // counter restarts at 1, so runIndex keeps same-turn responses from different
    // runs in distinct slots (and thus in the right order).
    let runIndex = 0
    // A non-passing eval is only a boundary if another agent round follows. The
    // terminal aggregate for a max-rounds eval run arrives after the last failed
    // verdict; delaying this bump lets it merge back into the final round instead
    // of rendering as a separate assistant bubble on rebuild.
    let pendingEvalBoundary = false

    function turnKey(msg: ChatMessage, turn: number): string {
      if (!turn) return ''
      return [
        runIndex,
        msg.session_id,
        msg.agent_id || msg.agent_name || pendingAgentName || 'agent',
        turn,
      ].join('-')
    }

    function ensureResponse(timestamp: number, agentName?: string, turn?: number, key?: string): TimelineItem {
      const resolvedAgentName = agentName || pendingAgentName
      if (key) {
        const existing = responsesByTurn.get(key)
        if (existing?.assistantResponse) {
          existing.agentName = resolvedAgentName || existing.agentName
          existing.assistantResponse.agentName = resolvedAgentName || existing.assistantResponse.agentName
          existing.assistantResponse.turn = turn || existing.assistantResponse.turn
          return existing
        }
      } else if (currentResponse?.assistantResponse) {
        currentResponse.agentName = resolvedAgentName || currentResponse.agentName
        currentResponse.assistantResponse.agentName = resolvedAgentName || currentResponse.assistantResponse.agentName
        currentResponse.assistantResponse.turn = turn || currentResponse.assistantResponse.turn
        return currentResponse
      }

      const id = key ? `assistant-history-${key}` : `assistant-history-${timestamp}-${built.length}`
      currentResponse = {
        id,
        kind: 'assistant_response',
        timestamp,
        agentName: resolvedAgentName,
        assistantResponse: {
          id,
          turn,
          agentName: resolvedAgentName,
          tools: [],
          streaming: false,
        },
      }
      built.push(currentResponse)
      if (key) responsesByTurn.set(key, currentResponse)
      return currentResponse
    }

    function beginNextEvalRound() {
      if (!pendingEvalBoundary) return
      currentResponse = null
      pendingAgentName = undefined
      runIndex++
      pendingEvalBoundary = false
    }

    function hasEvalBeforeNextUser(startIndex: number): boolean {
      for (let j = startIndex + 1; j < msgs.length; j++) {
        const next = msgs[j]
        if (next.role === 'user') return false
        if (metadataString(next.metadata, 'event_type') === 'eval') return true
      }
      return false
    }

    function toolMapKey(key: string, toolID: string): string {
      return key ? `${key}:${toolID}` : toolID
    }

    for (let i = 0; i < msgs.length; i++) {
      const msg = msgs[i]
      const timestamp = new Date(msg.created_at).getTime()
      const eventType = metadataString(msg.metadata, 'event_type')
      const turn = metadataNumber(msg.metadata, 'turn')
      let key = turnKey(msg, turn)

      if (msg.role === 'tool_call') {
        beginNextEvalRound()
        key = turnKey(msg, turn)
        const response = ensureResponse(timestamp, msg.agent_name, turn, key)
        const tcID = metadataString(msg.metadata, 'tool_call_id') || msg.id
        const toolCall = {
            id: tcID,
            toolName: metadataString(msg.metadata, 'tool_name') || '',
            toolArgs: metadataString(msg.metadata, 'tool_args') || msg.content,
            pending: true,
        }
        const tools = response.assistantResponse!.tools
        const existingIndex = tools.findIndex((tool) => tool.id === tcID)
        if (existingIndex >= 0) {
          tools[existingIndex] = { ...tools[existingIndex], ...toolCall }
          toolsByID.set(toolMapKey(key, tcID), tools[existingIndex])
        } else {
          tools.push(toolCall)
          toolsByID.set(toolMapKey(key, tcID), toolCall)
        }
        pendingAgentName = undefined
        continue
      }

      if (msg.role === 'tool_result') {
        beginNextEvalRound()
        key = turnKey(msg, turn)
        const tcID = metadataString(msg.metadata, 'tool_call_id')
        const existing = tcID ? toolsByID.get(toolMapKey(key, tcID)) || toolsByID.get(tcID) : undefined
        if (existing) {
          existing.result = msg.content
          existing.pending = false
        } else {
          const response = ensureResponse(timestamp, msg.agent_name, turn, key)
          response.assistantResponse!.tools.push({
            id: tcID || msg.id,
            toolName: 'tool',
            toolArgs: '',
            result: msg.content,
            pending: false,
          })
        }
        pendingAgentName = undefined
        continue
      }

      if (eventType === 'thinking') {
        const content = msg.content === 'thinking' ? '' : msg.content
        if (!content.trim()) continue
        beginNextEvalRound()
        key = turnKey(msg, turn)
        const response = ensureResponse(timestamp, msg.agent_name, turn, key)
        response.assistantResponse!.thinking = content
        pendingAgentName = undefined
        continue
      }

      if (eventType === 'agent_joined') {
        beginNextEvalRound()
        pendingAgentName = msg.agent_name || msg.content.replace(/\s+joined$/, '')
        continue
      }

      if (eventType === 'scan_complete') {
        beginNextEvalRound()
        const scanID = metadataString(msg.metadata, 'scan_id')
        if (!scanID) continue
        // The heavy Result isn't persisted in the message — the card pulls it from
        // the scanResults map (loaded from the session's scan_ids on activation),
        // so this item only needs to carry the scan_id. Same id as the live append
        // so a rebuild that races the live event upserts instead of duplicating.
        built.push({
          id: `scanres-${scanID}`,
          kind: 'scan_complete',
          timestamp,
          scanID,
        })
        continue
      }

      if (eventType === 'eval') {
        beginNextEvalRound()
        const pass = metadataBool(msg.metadata, 'eval_pass')
        built.push({
          id: msg.id,
          kind: 'eval',
          timestamp,
          evalRound: metadataNumber(msg.metadata, 'eval_round'),
          evalPass: pass,
          evalReason: metadataString(msg.metadata, 'eval_reason') || msg.content,
        })
        // Do not bump immediately. If this was the last failed verdict, the next
        // persisted assistant message is the terminal aggregate for the same
        // round. The next visible agent event (or the next eval verdict) consumes
        // the boundary through beginNextEvalRound().
        pendingEvalBoundary = !pass
        continue
      }

      if (msg.role === 'assistant') {
        // A persisted assistant after a failed eval is usually the terminal
        // aggregate for the just-finished max-rounds run. Keep it in the same run
        // unless another eval verdict appears before the next user message, which
        // means this assistant belongs to an intervening round.
        if (pendingEvalBoundary && hasEvalBeforeNextUser(i)) {
          beginNextEvalRound()
        } else {
          pendingEvalBoundary = false
        }
        key = turnKey(msg, turn)
        const response = ensureResponse(timestamp, msg.agent_name, turn, key)
        response.assistantResponse!.response = msg
        response.assistantResponse!.streaming = false
        currentResponse = null
        pendingAgentName = undefined
        continue
      }

      if (msg.role === 'user') {
        pendingEvalBoundary = false
        currentResponse = null
        pendingAgentName = undefined
        runIndex++
      }

      built.push({
        id: msg.id,
        kind: 'message' as TimelineItemKind,
        timestamp,
        message: msg,
      })
    }

    return built
  }

  // Chat SSE has no server-side backlog, so a terminal event lost during an
  // EventSource reconnect would strand the composer as "busy" forever. On each
  // SSE connection error, reconcile against persisted truth: if the run's
  // aggregate assistant reply is already the tail, the turn ended during the gap
  // — rebuild the timeline from messages (which clears streaming) and release the
  // composer. If the tail is still the user's message (or a mid-run tool step),
  // the run is in flight; leave it for the reconnected stream to finish. This is
  // conservative by design — it never finalizes a turn that hasn't persisted its
  // reply — and it's idempotent, so firing on every reconnect attempt is safe.
  async function reconcileAfterReconnect(id: string) {
    if (id !== activeSessionRef.current) return
    const activation = activationRef.current
    try {
      const msgs = await listChatMessages(id)
      if (activation !== activationRef.current || id !== activeSessionRef.current) return
      const last = msgs[msgs.length - 1]
      if (!last || last.role !== 'assistant') return
      setMessages(msgs)
      const rebuilt = buildTimelineFromMessages(msgs)
      timelineRef.current = rebuilt
      setTimeline(rebuilt)
      finalizeRun()
    } catch {}
  }

  async function activateSession(id: string, route: RouteMode) {
    const activation = ++activationRef.current
    closeSubscription()
    // Paint the last-seen conversation from cache synchronously — this runs
    // before the first await, so React batches it with the state below into a
    // single render and the panel jumps straight to the cached messages instead
    // of flashing blank while we revalidate. A cold session has no snapshot yet,
    // so it clears to empty and waits for the fetch as before.
    const cached = sessionCacheRef.current.get(id)
    if (cached) restoreSnapshot(cached)
    else resetSessionState()
    setActiveSessionID(id)
    // Mirror into the ref synchronously so a send issued immediately after
    // activation (e.g. the deck's Command Cortex) targets the new session
    // without waiting for the activeSessionID effect to flush on re-render.
    activeSessionRef.current = id
    setSessionRoute(id, route)

    try {
      const msgs = await listChatMessages(id)
      if (activation !== activationRef.current) return
      // On a cache hit the painted messages are almost always still current;
      // skip the setState + timeline rebuild (main-thread work that grows with
      // history length) unless the server actually has something new.
      if (!cached || messagesDiffer(cached.messages, msgs)) {
        setMessages(msgs)
        const builtTimeline = buildTimelineFromMessages(msgs)
        timelineRef.current = builtTimeline
        setTimeline(builtTimeline)
      }

      const session = await getChatSession(id)
      if (activation !== activationRef.current) return
      if (session.scan_ids && session.scan_ids.length) {
        // Fetch every linked scan at once instead of awaiting them one after
        // another — a session with N scans used to cost N serial round-trips
        // before its results deck filled in.
        const loaded = await Promise.all(
          session.scan_ids.map(async (scanID) => {
            try {
              const scan = await getScan(scanID)
              return { scanID, result: scan.result }
            } catch {
              return { scanID, result: undefined as ScanResult | undefined }
            }
          }),
        )
        // A session switch during scan loading bumps activationRef; discard
        // these stale results instead of writing them into the new session's
        // scanResults map.
        if (activation !== activationRef.current) return
        const withResult = loaded.filter((e) => e.result)
        if (withResult.length) {
          setScanResults((prev) => {
            const next = new Map(prev)
            for (const e of withResult) next.set(e.scanID, e.result!)
            return next
          })
        }
      }
    } catch {}

    if (activation !== activationRef.current) return
    unsubRef.current = subscribeChatEvents(id, handleChatEvent, () => reconcileAfterReconnect(id))
  }

  async function handleCreateSession(agentID: string) {
    try {
      const session = await createChatSession(agentID)
      const a = agentsRef.current.find((x) => x.id === agentID)
      if (a) selectedNodeKeyRef.current = agentNodeKey(a)
      setSelectedAgentID(agentID)
      await refreshSessions()
      await activateSession(session.id, 'push')
    } catch (err: any) {
      setError(err.message || 'Failed to create session')
    }
  }

  async function handleDeleteSession(id: string) {
    try {
      await deleteChatSession(id)
      sessionCacheRef.current.delete(id)
      if (activeSessionID === id) {
        activationRef.current++
        closeSubscription()
        resetSessionState()
        setActiveSessionID(null)
        window.history.pushState({}, '', '/')
      }
      await refreshSessions()
    } catch (err: any) {
      setError(err.message || 'Failed to delete session')
    }
  }

  async function handleSendMessage(content: string, opts?: { persist?: boolean; evalCriteria?: string; evalMaxRounds?: number }) {
    const sessionID = activeSessionRef.current
    if (!sessionID) return
    const trimmed = content.trim()
    if (!trimmed) return

    const msgID = safeUUID()
    userMsgIdsRef.current.add(msgID)
    activeTurnRef.current = null
    runEpochRef.current += 1

    const optimistic: ChatMessage = {
      id: msgID,
      session_id: sessionID,
      role: 'user',
      content: trimmed,
      created_at: new Date().toISOString(),
    }
    setMessages((prev) => [...prev, optimistic])
    appendTimeline({
      id: msgID,
      kind: 'message',
      timestamp: Date.now(),
      message: optimistic,
    })
    setError('')
    setPendingResponse(true)

    try {
      const serverMsg = await sendChatMessage(sessionID, trimmed, opts)
      userMsgIdsRef.current.add(serverMsg.id)
      await refreshSessions()
    } catch (err: any) {
      setPendingResponse(false)
      setError(err.message || 'Failed to send message')
    }
  }

  // Make sure a chat session is active, lazily creating one on the selected (or
  // first connected) node if none is open. Returns the session id, or null if
  // no node is connected / creation failed (error already surfaced). Factored
  // out of handleCommand so the asset-pool "reference" flow can seed a composer
  // draft into a guaranteed-live session without also sending a message.
  async function ensureSession(): Promise<string | null> {
    if (activeSessionRef.current) return activeSessionRef.current
    // Prefer the selected node only while it's actually connected; a selection
    // left dangling by a node that went away falls back to the first agent.
    const connected = agents.find((a) => a.id === selectedAgentID)
    const agentID = connected?.id || agents[0]?.id
    if (!agentID) {
      setError('No node connected — launch a local agent or connect one first.')
      return null
    }
    try {
      const session = await createChatSession(agentID)
      setSelectedAgentID(agentID)
      await refreshSessions()
      await activateSession(session.id, 'push')
      return session.id
    } catch (err: any) {
      setError(err.message || 'Failed to start session')
      return null
    }
  }

  // Deck "Command Cortex" entrypoint: route a free-form command from the
  // operation deck into the chat workspace. When no session is open yet it
  // spins one up on the active node first, so the typed text is never dropped.
  async function handleCommand(content: string) {
    const trimmed = content.trim()
    if (!trimmed) return
    if (!(await ensureSession())) return
    await handleSendMessage(trimmed)
  }

  // Channel-2 "quick dispatch": fire a target at an agent in its OWN fresh
  // session (titled with the target), auto-sending the prompt. Deliberately
  // bypasses handleSendMessage — that only targets the ACTIVE session and writes
  // an optimistic bubble, neither of which fits a background dispatch. Returns
  // the new session (or null if no node is connected / it failed).
  async function quickDispatch(
    target: string,
    prompt: string,
    agentID?: string,
    opts?: { activate?: boolean; skipRefresh?: boolean },
  ): Promise<ChatSession | null> {
    const connected = agents.find((a) => a.id === selectedAgentID)
    const aID = agentID || connected?.id || agents[0]?.id
    if (!aID) {
      setError('No node connected — launch a local agent or connect one first.')
      return null
    }
    try {
      const session = await createChatSession(aID, target)
      await sendChatMessage(session.id, prompt)
      if (!opts?.skipRefresh) await refreshSessions()
      if (opts?.activate) {
        setSelectedAgentID(aID)
        await activateSession(session.id, 'push')
      }
      return session
    } catch (err: any) {
      setError(err.message || 'Failed to dispatch agent')
      return null
    }
  }

  // Scan-deck AI actions (数据分析 / 资产评估 / 复测): each opens its OWN fresh
  // session — linked to the originating scan and titled by kind — activates it
  // (routing to the chat workspace), then auto-sends the seed prompt so the
  // agent's streaming run IS the process. The scan deck reverse-finds this
  // session by scan_id to mirror its final conclusion back. Returns the new
  // session id, or null if no node is connected / it failed.
  async function startReportSession(args: {
    title: string
    seedPrompt: string
    scanID?: string
  }): Promise<string | null> {
    const connected = agents.find((a) => a.id === selectedAgentID)
    const agentID = connected?.id || agents[0]?.id
    if (!agentID) {
      setError('No node connected — launch a local agent or connect one first.')
      return null
    }
    try {
      const session = await createChatSession(agentID, args.title, args.scanID)
      setSelectedAgentID(agentID)
      await refreshSessions()
      await activateSession(session.id, 'push')
      await handleSendMessage(args.seedPrompt)
      return session.id
    } catch (err: any) {
      setError(err.message || 'Failed to start session')
      return null
    }
  }

  // Channel-2 batch fan-out: one fresh session per target, distributed across
  // the connected fleet round-robin so independent nodes run in parallel (a lone
  // node just serializes them on its own task queue). Concurrency-capped so
  // selecting a large pool doesn't fire hundreds of requests at once.
  async function batchQuickDispatch(items: { target: string; prompt: string }[]) {
    const fleet = agents
    if (fleet.length === 0) {
      setError('No node connected — launch a local agent or connect one first.')
      return
    }
    const CONCURRENCY = 6
    for (let i = 0; i < items.length; i += CONCURRENCY) {
      const batch = items.slice(i, i + CONCURRENCY)
      await Promise.all(
        batch.map((it, j) =>
          quickDispatch(it.target, it.prompt, fleet[(i + j) % fleet.length].id, { skipRefresh: true }),
        ),
      )
    }
    await refreshSessions()
  }

  async function handleCancelMessage() {
    const sessionID = activeSessionRef.current
    if (!sessionID) return
    finalizeRun()
    try {
      await cancelChatSession(sessionID)
      await refreshSessions()
    } catch (err: any) {
      setError(err.message || 'Failed to pause response')
    }
  }

  useEffect(() => {
    const applyRoute = () => {
      const route = parseRoute(window.location.pathname)
      if (route.kind === 'session') {
        void activateSession(route.id, 'none')
      } else if (route.kind === 'scan') {
        // This hook owns session routes; the scan deck (and its routes) are gone.
        return
      } else if (isRootPath(window.location.pathname)) {
        activationRef.current++
        closeSubscription()
        resetSessionState()
        setActiveSessionID(null)
      }
    }
    applyRoute()
    window.addEventListener('popstate', applyRoute)
    return () => {
      window.removeEventListener('popstate', applyRoute)
      closeSubscription()
    }
  }, [])

  const clearError = useCallback(() => setError(''), [])

  return {
    agents,
    selectedAgentID,
    sessions,
    activeSessionID,
    timeline,
    scanResults,
    isThinking,
    busy: pendingResponse || isThinking || timeline.some((item) => (
      item.kind === 'assistant_response' && item.assistantResponse?.streaming
    )),
    error,
    selectAgent: (id: string) => {
      const a = agentsRef.current.find((x) => x.id === id)
      selectedNodeKeyRef.current = a ? agentNodeKey(a) : null
      setSelectedAgentID(id)
    },
    createSession: handleCreateSession,
    selectSession: (id: string) => activateSession(id, 'push'),
    deleteSession: handleDeleteSession,
    sendMessage: handleSendMessage,
    command: handleCommand,
    ensureSession,
    quickDispatch,
    startReportSession,
    batchQuickDispatch,
    cancelMessage: handleCancelMessage,
    clearError,
  }
}

function isDuplicateAssistantTimelineItem(item: TimelineItem | undefined, msg: ChatMessage, timestamp: number): boolean {
  if (!item?.message) return false
  if (timestamp - item.timestamp > 15000) return false
  return isDuplicateAssistantMessage(item.message, msg)
}

function isDuplicateAssistantMessage(prev: ChatMessage | undefined, next: ChatMessage): boolean {
  if (!prev || next.role !== 'assistant' || prev.role !== 'assistant') return false
  if (normalizeMessageContent(prev.content) !== normalizeMessageContent(next.content)) return false
  return (prev.agent_name || '') === (next.agent_name || '')
}

function isDuplicateAssistantResponseContent(item: TimelineItem | undefined, content: string): boolean {
  if (!item?.assistantResponse?.response) return false
  return normalizeMessageContent(item.assistantResponse.response.content) === normalizeMessageContent(content)
}

function normalizeMessageContent(value: string): string {
  return value.replace(/\s+/g, ' ').trim()
}

function metadataString(metadata: Record<string, unknown> | undefined, key: string): string {
  const value = metadata?.[key]
  return typeof value === 'string' ? value : ''
}

function metadataNumber(metadata: Record<string, unknown> | undefined, key: string): number {
  const value = metadata?.[key]
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string') {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : 0
  }
  return 0
}

function metadataBool(metadata: Record<string, unknown> | undefined, key: string): boolean {
  const value = metadata?.[key]
  if (typeof value === 'boolean') return value
  return value === 'true' || value === 1
}
