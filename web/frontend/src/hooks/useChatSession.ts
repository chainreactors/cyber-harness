import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { anyUnpack } from '@bufbuild/protobuf/wkt'
import { ScanStatus, SessionScanEventSchema, WebMessageMetadataSchema } from '../cyber-proto'
import { usePolling } from './usePolling'
import {
  cancelChatSession,
  closeChatSession,
  createChatSession,
  deleteChatSession,
  executeChatCommand,
  getChatSession,
  listAgents,
  listChatMessages,
  listChatSessions,
  updateChatSession,
  type SessionFilters,
  resetChatSession,
  sendChatMessage,
  subscribeAOPEvents,
} from '../api'
import type { AgentView, AOPEvent, AOPSession, ChatSendOptions, EventDelivery, SCONode, SessionRecord } from '../api'
import { listSCONodes, syncCSTXArtifacts } from '../lib/cstx-runtime'
import {
  isRootPath,
  parseRoute,
  setSessionRoute,
  type RouteMode,
} from '../lib/route'

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

function aopExtension(event: AOPEvent): Record<string, unknown> | undefined {
  for (const extension of event.extensions) {
    const value = anyUnpack(extension, WebMessageMetadataSchema)
    if (value) return { nodeId: value.nodeId, code: value.code, params: value.params, agentList: value.agentList }
  }
  return undefined
}

export type TimelineItemKind = 'message' | 'scan_complete' | 'thinking'

// ChatMessage is the flat render model the chat UI projects from the AOP event
// log (listChatMessages returns raw EventDelivery records). It is a view model
// owned by this hook, not an API wire type — the wire truth is aop.Event +
// EventDelivery + the cyber.web extension.
export interface ChatMessage {
  id: string
  session_id: string
  role: 'user' | 'assistant' | 'system'
  node_id?: string
  agent_name?: string
  content: string
  metadata?: Record<string, unknown>
  created_at: string
  cursor?: number
  turn_id?: string
}

function timestampToISOString(value?: { seconds: bigint; nanos: number }): string {
  if (!value) return new Date(0).toISOString()
  return new Date(Number(value.seconds) * 1000 + Math.floor(value.nanos / 1_000_000)).toISOString()
}

// Project one AOP delivery into the flat render model. Non-message payloads
// (turn lifecycle, tool calls, …) return null — the AOP stream renders those.
function deliveryToChatMessage(delivery: EventDelivery): ChatMessage | null {
  const event = delivery.event
  if (!event || event.payload.case !== 'message') return null
  const message = event.payload.value
  const text = message.content
    .filter((part) => part.value.case === 'text')
    .map((part) => part.value.case === 'text' ? part.value.value.text : '')
    .join('\n')
  let metadata: Record<string, unknown> | undefined
  let nodeID: string | undefined
  for (const extension of event.extensions) {
    const decoded = anyUnpack(extension, WebMessageMetadataSchema)
    if (decoded) {
      nodeID = decoded.nodeId || undefined
      metadata = { code: decoded.code, params: decoded.params, agentList: decoded.agentList }
      break
    }
  }
  const role = message.role === 'assistant' || message.role === 'system' ? message.role : 'user'
  return {
    id: message.id,
    session_id: event.sessionId,
    role,
    node_id: nodeID,
    agent_name: event.emitter,
    content: text,
    metadata,
    created_at: timestampToISOString(event.emittedAt),
    cursor: delivery.cursor ? Number(delivery.cursor) : undefined,
    turn_id: event.turnId || undefined,
  }
}

export interface TimelineItem {
  id: string
  kind: TimelineItemKind
  timestamp: number
  message?: ChatMessage
  scanID?: string
  scanNodes?: SCONode[]
  agentName?: string
  content?: string
}

// A per-session snapshot of the durable conversation state — everything the
// panel renders that survives a switch away and back. Cached in memory so a
// revisit repaints instantly instead of flashing blank for a network fetch.
interface SessionSnapshot {
  messages: ChatMessage[]
  timeline: TimelineItem[]
  scanResults: Map<string, SCONode[]>
}

// Deterministic roster order. The hub returns agents in Go-map iteration order,
// which is randomized per request; without a stable sort the sidebar reshuffles
// on every 5s poll. Ordering by node URI keeps the list — and any "first agent"
// auto-pick — put across refreshes.
function sortAgentsByNode(list: AgentView[]): AgentView[] {
  return [...list].sort((a, b) => (a.hello?.nodeId || '').localeCompare(b.hello?.nodeId || ''))
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

function readSessionFilters(): SessionFilters {
  const params = new URLSearchParams(window.location.search)
  return { search: params.get('search') || '', nodeId: params.get('node') || '', archived: params.get('archived') === 'true' }
}

export function useChatSession() {
  const { t } = useTranslation('chat')
  const [agents, setAgents] = useState<AgentView[]>([])
  const [selectedNodeID, setSelectedNodeID] = useState<string | null>(null)
  const [sessionFilters, setSessionFilters] = useState(readSessionFilters)
  const sessionQueryVersion = useRef(0)
  const [sessions, setSessions] = useState<SessionRecord[]>([])
  const [activeSessionRecord, setActiveSessionRecord] = useState<SessionRecord | null>(null)
  const [activeSessionID, setActiveSessionID] = useState<string | null>(null)
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [timeline, setTimeline] = useState<TimelineItem[]>([])
  const [aopEvents, setAOPEvents] = useState<AOPEvent[]>([])
  const timelineRef = useRef<TimelineItem[]>([])
  const [scanResults, setScanResults] = useState<Map<string, SCONode[]>>(() => new Map())
  const [isThinking, setIsThinking] = useState(false)
  const [runRequestPending, setRunRequestPending] = useState(false)
  const [activeTurnID, setActiveTurnID] = useState('')
  const [error, setError] = useState('')
  const creatingSession = useRef<Promise<string | null> | null>(null)
  const retrySessionCreation = useRef<{ nodeID: string; sessionID: string; requestID: string } | null>(null)
  const retrySubmission = useRef<{ signature: string; messageID: string; requestID: string; turnID: string } | null>(null)
  const submittingRef = useRef(false)
  const unsubRef = useRef<(() => void) | null>(null)
  const activationRef = useRef(0)
  const activeSessionRef = useRef<string | null>(null)
  const activeTurnRef = useRef<string>('')
  const endedTurnIDsRef = useRef<Set<string>>(new Set())
  const seenEventIDsRef = useRef<Set<string>>(new Set())
  // Latest roster mirrors `agents` for event handlers that run between renders.
  // selectedNodeID is the Web-scoped node_id; no second identity or
  // reconnect remapping state is needed.
  const agentsRef = useRef<AgentView[]>([])
  const sessionCacheRef = useRef<Map<string, SessionSnapshot>>(new Map())

  useEffect(() => {
    activeSessionRef.current = activeSessionID
  }, [activeSessionID])

  // Mirror the active session's durable state into an in-memory cache keyed by
  // session id. activateSession repaints this snapshot synchronously on
  // re-entry, so switching back to a session jumps straight to its conversation
  // instead of blanking for a round-trip. Writing on every durable change (vs.
  // snapshotting on leave) keeps the cache live with streamed Connect updates
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
      setSelectedNodeID((current) => {
        // node_id survives reconnects. Keep an absent selection so a temporary
        // disconnect does not silently retarget the operator to another node.
        return current || list[0]?.hello?.nodeId || null
      })
    } catch {}
  }, [])

  const refreshSessions = useCallback(async () => {
    const version = ++sessionQueryVersion.current
    try {
      const records = await listChatSessions({ search: sessionFilters.search, archived: sessionFilters.archived })
      if (version === sessionQueryVersion.current) setSessions(records)
    } catch (error) { if (version === sessionQueryVersion.current) setError(String(error)) }
  }, [sessionFilters.search, sessionFilters.archived])

  function filterSessions(patch: Partial<SessionFilters>) {
    const next = { ...sessionFilters, ...patch }
    const url = new URL(window.location.href)
    url.searchParams.delete('target')
    url.searchParams.delete('view')
    for (const [key, value] of Object.entries({ search: next.search, node: next.nodeId, archived: next.archived ? 'true' : '' })) {
      if (value) url.searchParams.set(key, value); else url.searchParams.delete(key)
    }
    window.history.replaceState({}, '', url)
    setSessionFilters(next)
  }

  async function updateSession(id: string, patch: { title?: string; archived?: boolean }) {
    try { const record = await updateChatSession(id, patch); if (activeSessionRef.current === id) setActiveSessionRecord(record); await refreshSessions() }
    catch (error) { setError(String(error)); throw error }
  }

  useEffect(() => {
    const restore = () => setSessionFilters(readSessionFilters())
    window.addEventListener('popstate', restore)
    const url = new URL(window.location.href)
    if (url.searchParams.has('view') || url.searchParams.has('target')) {
      url.searchParams.delete('view')
      url.searchParams.delete('target')
      window.history.replaceState({}, '', url)
    }
    return () => window.removeEventListener('popstate', restore)
  }, [])

  useEffect(() => {
    refreshAgents()
    refreshSessions()
  }, [refreshAgents, refreshSessions])
  // Roster poll — paused while the tab is hidden (this runs for the whole app
  // lifetime, so a backgrounded tab would otherwise keep issuing AgentService queries
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
    activeTurnRef.current = ''
    endedTurnIDsRef.current.clear()
    seenEventIDsRef.current.clear()
    setActiveTurnID('')
    setIsThinking(false)
    setRunRequestPending(false)
    setError('')
  }

  function resetSessionState() {
    setMessages([])
    timelineRef.current = []
    setTimeline([])
    setAOPEvents([])
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
    // A new WatchEvents subscription starts at cursor zero and replays the
    // complete AOP history. Avoid restoring another AOP copy from the cache.
    setAOPEvents([])
    setScanResults(snap.scanResults)
    resetTransientState()
  }

  function appendTimeline(item: TimelineItem) {
    setTimelineItems((prev) => [...prev, item])
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

  function activateTurn(turnID: string) {
    const id = turnID.trim()
    activeTurnRef.current = id
    setActiveTurnID(id)
  }

  function isCurrentSession(sessionID: string, activation: number): boolean {
    return activation === activationRef.current && activeSessionRef.current === sessionID
  }

  // A Run converges only on turn_ended. Session lifecycle is independent and a
  // turn-scoped error is diagnostic until its terminal turn_ended arrives.
  // Ignore a late terminal event for an older turn so it cannot clear a newer
  // active run after reconnect/replay interleaving.
  function finalizeRun(turnID = '') {
    const id = turnID.trim()
    if (id) endedTurnIDsRef.current.add(id)
    if (id && activeTurnRef.current && activeTurnRef.current !== id) return
    activateTurn('')
    setIsThinking(false)
    setRunRequestPending(false)
  }

  function handleAOPEvent(event: AOPEvent) {
    // Replay must not repeat lifecycle side effects either: a duplicate old
    // turnStarted can otherwise resurrect a completed turn after reconnect.
    if (event.id && seenEventIDsRef.current.has(event.id)) return
    if (event.id) seenEventIDsRef.current.add(event.id)
    setAOPEvents((previous) => [...previous, event])
    switch (event.payload.case) {
      case 'turnStarted':
        endedTurnIDsRef.current.delete(event.turnId)
        activateTurn(event.turnId)
        setRunRequestPending(false)
        setIsThinking(true)
        break
      case 'messageDelta':
        setIsThinking(event.payload.value.value.case === 'reasoning')
        break
      case 'toolCall':
        setIsThinking(false)
        break
      case 'message':
        // Messages carry content, not lifecycle. Command results are durable
        // assistant messages with no turn_id and must never reactivate the Run
        // state during live delivery or history replay.
        if (event.payload.value.role === 'assistant') setIsThinking(false)
        break
      case 'turnEnded':
        finalizeRun(event.turnId)
        break
      case 'sessionEnded':
        break
      case 'extension': {
        const extension = event.payload.value
        try {
          const scan = anyUnpack(extension, SessionScanEventSchema)
          if (!scan) break
          if (!scan.scanId || ![ScanStatus.COMPLETED, ScanStatus.FAILED, ScanStatus.CANCELED].includes(scan.status)) break
          const timelineID = `scanres-${scan.scanId}`
          setTimelineItems((previous) => previous.some((item) => item.id === timelineID)
            ? previous
            : [...previous, { id: timelineID, kind: 'scan_complete', timestamp: Date.now(), scanID: scan.scanId }])
          const sessionID = activeSessionRef.current
          void syncCSTXArtifacts().then(() => listSCONodes({ scanId: scan.scanId })).then(({ items: nodes }) => {
            if (activeSessionRef.current !== sessionID) return
            setScanResults((previous) => new Map(previous).set(scan.scanId, nodes))
            updateTimelineItem(timelineID, (item) => ({ ...item, scanNodes: nodes }))
          }).catch((error) => { if (activeSessionRef.current === sessionID) setError(String(error)) })
        } catch {
          // Ignore malformed application extensions; the AOP stream remains usable.
        }
        break
      }
      case 'error': {
        const data = event.payload.value
        // Hub-originated failures carry a translatable code plus i18n params
        // in the cyber.web extension; agent errors are plain text.
        const params = aopExtension(event)?.params as Record<string, unknown> | undefined
        if (data.code) setError(t(`sys.${data.code}`, { ...(params || {}), defaultValue: data.message || '' }))
        else setError(String(data.message ?? 'Agent error'))
        if (!event.turnId) finalizeRun()
        break
      }
    }
  }

  // Rebuild the platform timeline from persisted messages. Assistant content is
  // NOT rebuilt here — WatchEvents replay is the sole source of agent history
  // (it carries the complete message/tool/status stream); this only restores the
  // user/system conversation shell shown before the replay arrives. Scan-result
  // cards are not messages: they arrive as an AOP extension, or are rebuilt from
  // the session's scan ids when no extension was ever emitted for it.
  function buildTimelineFromMessages(msgs: ChatMessage[]): TimelineItem[] {
    const built: TimelineItem[] = []
    for (const msg of msgs) {
      const timestamp = new Date(msg.created_at).getTime()
      if (msg.role === 'assistant') continue
      built.push({ id: msg.id, kind: 'message', timestamp, message: msg })
    }
    return built
  }

  // WatchEvents reconnects from its last durable cursor. This extra reconciliation
  // refreshes the durable message projection while cursor replay catches up.
  // An assistant tail may be an intermediate tool step or Goal round; only
  // turnEnded from the event stream can settle the active run.
  async function reconcileAfterReconnect(id: string) {
    if (id !== activeSessionRef.current) return
    const activation = activationRef.current
    try {
      const msgs = (await listChatMessages(id)).flatMap((delivery) => deliveryToChatMessage(delivery) || [])
      if (activation !== activationRef.current || id !== activeSessionRef.current) return
      setMessages(msgs)
      const rebuilt = buildTimelineFromMessages(msgs)
      timelineRef.current = rebuilt
      setTimeline(rebuilt)
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
      const msgs = (await listChatMessages(id)).flatMap((delivery) => deliveryToChatMessage(delivery) || [])
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
      setActiveSessionRecord(session)
      const scanIDs = Array.isArray(session.extensions.scan?.ids)
        ? session.extensions.scan.ids.filter((id): id is string => typeof id === 'string')
        : []
      if (scanIDs.length) {
        // Keep status cards visible even if archive sync or parsing fails.
        setTimelineItems((previous) => {
          const next = [...previous]
          for (const scanID of scanIDs) {
            const id = `scanres-${scanID}`
            if (!next.some((item) => item.id === id)) next.push({ id, kind: 'scan_complete', timestamp: Date.now(), scanID })
          }
          return next
        })
        await syncCSTXArtifacts()
        if (activation !== activationRef.current) return
        // Read every linked scan's CSTX nodes together after the archive sync.
        const loaded = await Promise.all(
          scanIDs.map(async (scanID) => {
            try {
              const { items: nodes } = await listSCONodes({ scanId: scanID })
              return { scanID, nodes }
            } catch (error) {
              if (activation === activationRef.current) setError(String(error))
              return { scanID, nodes: undefined as SCONode[] | undefined }
            }
          }),
        )
        // A session switch during scan loading bumps activationRef; discard
        // these stale results instead of writing them into the new session's
        // scanResults map.
        if (activation !== activationRef.current) return
        const withResult = loaded.filter((e) => e.nodes !== undefined)
        if (withResult.length) {
          setScanResults((prev) => {
            const next = new Map(prev)
            for (const e of withResult) next.set(e.scanID, e.nodes!)
            return next
          })
          // A scan that finished while this session was already bound replays its
          // persisted extension, so a card normally arrives from the stream. A
          // session bound after the scan already finished never got one — the
          // fan-out only runs once, at completion — and its card exists nowhere
          // else. Rebuild one per linked scan that still has SCO nodes, keyed like
          // the live path so a replayed extension can't double-render it.
          setTimelineItems((prev) => {
            const next = [...prev]
            for (const e of withResult) {
              const id = `scanres-${e.scanID}`
              if (next.some((item) => item.id === id)) continue
              next.push({ id, kind: 'scan_complete', timestamp: Date.now(), scanID: e.scanID, scanNodes: e.nodes })
            }
            return next
          })
        }
      }
    } catch (error) {
      if (activation === activationRef.current) setError(String(error))
    }

    if (activation !== activationRef.current) return
    unsubRef.current = subscribeAOPEvents(
      id,
      handleAOPEvent,
      () => reconcileAfterReconnect(id),
    )
  }

  async function handleCreateSession(nodeID: string) {
    try {
      const session = await createChatSession(nodeID)
      setSelectedNodeID(nodeID)
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

  async function handleSendMessage(content: string, opts?: ChatSendOptions & { sessionID?: string }): Promise<boolean> {
    if ((!content.trim() && !opts?.images?.length) || submittingRef.current) return false
    submittingRef.current = true
    let optimisticID = ''
    let sessionID: string | null = null
    try {
      sessionID = opts?.sessionID || await ensureSession()
      if (!sessionID) return false
      const trimmed = content.trim()
      const lower = trimmed.toLowerCase()
      if (lower === '/clear') {
        const next = await resetChatSession(sessionID)
        if (next.session?.id) await activateSession(next.session.id, 'push')
        await refreshSessions()
        return true
      }
      if (lower === '/stop') { await handleCancelMessage(); return true }
      if (lower === '/exit' || lower === '/quit') { await closeChatSession(sessionID); await refreshSessions(); return true }
      const continueSession = lower === '/continue'
      const runContent = lower.startsWith('/followup ') ? trimmed.slice(trimmed.indexOf(' ') + 1).trim() : trimmed
      const command = !continueSession && (runContent.startsWith('!') || (runContent.startsWith('/') && !runContent.startsWith('/skill:') && !lower.startsWith('/followup ')))
      const signature = JSON.stringify([sessionID, runContent, { ...opts, images: opts?.images?.map((image) => [image.filename, image.mediaType, image.data.length]) }])
      if (retrySubmission.current?.signature !== signature) retrySubmission.current = { signature, messageID: safeUUID(), requestID: safeUUID(), turnID: safeUUID() }
      const submission = retrySubmission.current
      optimisticID = submission.messageID
      if (!continueSession && activeSessionRef.current === sessionID) {
        const message: ChatMessage = { id: optimisticID, session_id: sessionID, role: 'user', content: runContent, created_at: new Date().toISOString() }
        setMessages((previous) => previous.some((m) => m.id === message.id) ? previous : [...previous, message])
        setTimelineItems((previous) => previous.some((m) => m.id === message.id) ? previous : [...previous, { id: message.id, kind: 'message', timestamp: Date.now(), message }])
      }
      if (activeSessionRef.current === sessionID) { setError(''); setRunRequestPending(true) }
      if (command) await executeChatCommand(sessionID, runContent, submission.requestID)
      else {
        const sent = await sendChatMessage(sessionID, runContent, { ...opts, ...submission, continueSession })
        if (activeSessionRef.current === sessionID && sent.turnId && !endedTurnIDsRef.current.has(sent.turnId)) activateTurn(sent.turnId)
      }
      retrySubmission.current = null
      await refreshSessions()
      return true
    } catch (error) {
      if ((error as { rejected?: boolean }).rejected) retrySubmission.current = null
      if (activeSessionRef.current === sessionID) {
        setMessages((previous) => previous.filter((m) => m.id !== optimisticID))
        setTimelineItems((previous) => previous.filter((m) => m.id !== optimisticID))
      }
      setError(error instanceof Error ? error.message : 'Failed to send message')
      return false
    } finally { submittingRef.current = false; if (activeSessionRef.current === sessionID) setRunRequestPending(false) }
  }

  // Make sure a chat session is active, lazily creating one on the selected (or
  // first connected) node if none is open. Returns the session id, or null if
  // no node is connected / creation failed (error already surfaced). Factored
  // out of handleCommand so the asset-pool "reference" flow can seed a composer
  // draft into a guaranteed-live session without also sending a message.
  async function ensureSession(): Promise<string | null> {
    if (activeSessionRef.current) return activeSessionRef.current
    if (creatingSession.current) return creatingSession.current
    creatingSession.current = createSessionForInput().finally(() => { creatingSession.current = null })
    return creatingSession.current
  }

  async function createSessionForInput(): Promise<string | null> {
    // Prefer the selected node only while it's actually connected; a selection
    // left dangling by a node that went away falls back to the first agent.
    const connected = agents.find((a) => a.hello?.nodeId === selectedNodeID)
    const nodeID = connected?.hello?.nodeId || agents[0]?.hello?.nodeId
    if (!nodeID) {
      setError('No node connected — launch a local agent or connect one first.')
      return null
    }
    try {
      if (retrySessionCreation.current?.nodeID !== nodeID) retrySessionCreation.current = { nodeID, sessionID: safeUUID(), requestID: safeUUID() }
      const session = await createChatSession(nodeID, undefined, undefined, retrySessionCreation.current)
      retrySessionCreation.current = null
      setSelectedNodeID(nodeID)
      activeSessionRef.current = session.id
      await activateSession(session.id, 'push')
      await refreshSessions()
      return session.id
    } catch (err: any) {
      if (err.rejected) retrySessionCreation.current = null
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
    nodeID?: string,
    opts?: { activate?: boolean; skipRefresh?: boolean },
  ): Promise<AOPSession | null> {
    const connected = agents.find((a) => a.hello?.nodeId === selectedNodeID)
    const targetNodeID = nodeID || connected?.hello?.nodeId || agents[0]?.hello?.nodeId
    if (!targetNodeID) {
      setError('No node connected — launch a local agent or connect one first.')
      return null
    }
    try {
      const session = await createChatSession(targetNodeID, target)
      await sendChatMessage(session.id, prompt)
      if (!opts?.skipRefresh) await refreshSessions()
      if (opts?.activate) {
        setSelectedNodeID(targetNodeID)
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
    const connected = agents.find((a) => a.hello?.nodeId === selectedNodeID)
    const nodeID = connected?.hello?.nodeId || agents[0]?.hello?.nodeId
    if (!nodeID) {
      setError('No node connected — launch a local agent or connect one first.')
      return null
    }
    try {
      const session = await createChatSession(nodeID, args.title, args.scanID)
      setSelectedNodeID(nodeID)
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
          quickDispatch(it.target, it.prompt, fleet[(i + j) % fleet.length].hello?.nodeId, { skipRefresh: true }),
        ),
      )
    }
    await refreshSessions()
  }

  async function handleCancelMessage() {
    const sessionID = activeSessionRef.current
    const turnID = activeTurnRef.current
    const activation = activationRef.current
    if (!sessionID || !turnID) return
    try {
			await cancelChatSession(sessionID, turnID)
			// The request acknowledgement is not the turn terminal event. Keep the
			// run active until durable turnEnded arrives, including after reconnect.
			if (!isCurrentSession(sessionID, activation)) return
      await refreshSessions()
    } catch (err: any) {
			if (isCurrentSession(sessionID, activation)) setError(err.message || 'Failed to pause response')
    }
  }

  useEffect(() => {
    const applyRoute = () => {
      const route = parseRoute(window.location.pathname)
      if (route.kind === 'session') {
        void activateSession(route.id, 'none')
        return
      }
      // Any other path is a retired route (for example a /scans/<id> bookmark).
      // Nothing renders it, so show the session list and normalize the URL.
      if (!isRootPath(window.location.pathname)) {
        window.history.replaceState({}, '', '/')
      }
      activationRef.current++
      closeSubscription()
      resetSessionState()
      setActiveSessionID(null)
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
    selectedNodeID,
    sessions,
    sessionFilters, filterSessions, updateSession,
    activeSessionID,
    activeSessionRecord,
    timeline,
    aopEvents,
    scanResults,
    isThinking,
    busy: runRequestPending || activeTurnID !== '',
    canPause: activeTurnID !== '',
    error,
    selectNode: (nodeID: string) => {
      setSelectedNodeID(nodeID)
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
