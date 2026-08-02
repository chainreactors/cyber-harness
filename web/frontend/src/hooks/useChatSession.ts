import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { anyUnpack } from '@bufbuild/protobuf/wkt'
import { ScanStatus, SessionScanEventSchema, WebMessageMetadataSchema } from '../aiscan-proto'
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
  listSCONodes,
  resetChatSession,
  sendChatMessage,
  subscribeAOPEvents,
} from '../api'
import type { AgentView, AOPEvent, AOPSession, EventDelivery, SCONode, SessionRecord } from '../api'
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

function aopExtension(event: AOPEvent): Record<string, unknown> | undefined {
  for (const extension of event.extensions) {
    const value = anyUnpack(extension, WebMessageMetadataSchema)
    if (value) return { agentId: value.agentId, code: value.code, params: value.params }
  }
  return undefined
}

export type TimelineItemKind = 'message' | 'scan_started' | 'scan_progress' | 'scan_complete' | 'thinking'

// ChatMessage is the flat render model the chat UI projects from the AOP event
// log (listChatMessages returns raw EventDelivery records). It is a view model
// owned by this hook, not an API wire type — the wire truth is aop.Event +
// EventDelivery + the aiscan.web extension.
export interface ChatMessage {
  id: string
  session_id: string
  role: 'user' | 'assistant' | 'system'
  agent_id?: string
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
  let agentID: string | undefined
  for (const extension of event.extensions) {
    const decoded = anyUnpack(extension, WebMessageMetadataSchema)
    if (decoded) {
      agentID = decoded.agentId || undefined
      metadata = { code: decoded.code, params: decoded.params }
      break
    }
  }
  const role = message.role === 'assistant' || message.role === 'system' ? message.role : 'user'
  return {
    id: message.id,
    session_id: event.sessionId,
    role,
    agent_id: agentID,
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
  scanLines?: string[]
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
  return [...list].sort((a, b) => a.nodeUri.localeCompare(b.nodeUri))
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
  const [agents, setAgents] = useState<AgentView[]>([])
  const [selectedNodeURI, setSelectedNodeURI] = useState<string | null>(null)
  const [sessions, setSessions] = useState<SessionRecord[]>([])
  const [activeSessionID, setActiveSessionID] = useState<string | null>(null)
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [timeline, setTimeline] = useState<TimelineItem[]>([])
  const [aopEvents, setAOPEvents] = useState<AOPEvent[]>([])
  const timelineRef = useRef<TimelineItem[]>([])
  const [scanResults, setScanResults] = useState<Map<string, SCONode[]>>(() => new Map())
  const [isThinking, setIsThinking] = useState(false)
  const [pendingResponse, setPendingResponse] = useState(false)
  const [error, setError] = useState('')
  const unsubRef = useRef<(() => void) | null>(null)
  const activationRef = useRef(0)
  const activeSessionRef = useRef<string | null>(null)
  const activeTurnRef = useRef<string>('')
  // Latest roster mirrors `agents` for event handlers that run between renders.
  // selectedNodeURI is already the canonical node_uri; no second identity or
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
      setSelectedNodeURI((current) => {
        // node_uri survives reconnects. Keep an absent selection so a temporary
        // disconnect does not silently retarget the operator to another node.
        return current || list[0]?.nodeUri || null
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
	activeTurnRef.current = ''
    setIsThinking(false)
    setPendingResponse(false)
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

  // A Run converges only on turn_ended. Session lifecycle is independent and a
  // turn-scoped error is diagnostic until its terminal turn_ended arrives.
  function finalizeRun() {
	activeTurnRef.current = ''
    setIsThinking(false)
    setPendingResponse(false)
  }

  function handleAOPEvent(event: AOPEvent) {
    setAOPEvents((previous) => {
      if (event.id && previous.some((item) => item.id === event.id)) return previous
      return [...previous, event]
    })
    switch (event.payload.case) {
      case 'turnStarted':
		activeTurnRef.current = event.turnId
        setPendingResponse(true)
        setIsThinking(true)
        break
      case 'messageDelta':
      case 'toolCall':
        setPendingResponse(true)
        setIsThinking(false)
        break
      case 'turnEnded':
        finalizeRun()
        break
      case 'sessionEnded':
        break
      case 'extension': {
        const extension = event.payload.value
        try {
          const scan = anyUnpack(extension, SessionScanEventSchema)
          if (!scan) break
          if (!scan.scanId || scan.status !== ScanStatus.COMPLETED) break
          const timelineID = `scanres-${scan.scanId}`
          setTimelineItems((previous) => previous.some((item) => item.id === timelineID)
            ? previous
            : [...previous, { id: timelineID, kind: 'scan_complete', timestamp: Date.now(), scanID: scan.scanId }])
          // A completed scan's result is the SCO node set persisted under its
          // scan_id — load it so the timeline card can render.
          void listSCONodes({ scanId: scan.scanId, limit: 2000 }).then((nodes) => {
            setScanResults((previous) => new Map(previous).set(scan.scanId, nodes))
            updateTimelineItem(timelineID, (item) => ({ ...item, scanNodes: nodes }))
          }).catch(() => {})        } catch {
          // Ignore malformed product extensions; the AOP stream remains usable.
        }
        break
      }
      case 'error': {
        const data = event.payload.value
        // Hub-originated failures carry a translatable code plus i18n params
        // in the aiscan.web extension; agent errors are plain text.
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
  // platform artifacts the AOP stream doesn't render: scan-result cards
  // (persisted as system markers) and the user/system conversation shell shown
  // before the replay arrives.
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
  // is a conservative UI fallback when transport failure and component state
  // updates cross: a persisted assistant tail proves the run progressed far enough
  // to rebuild the visible message projection while cursor replay catches up.
  async function reconcileAfterReconnect(id: string) {
    if (id !== activeSessionRef.current) return
    const activation = activationRef.current
    try {
      const msgs = (await listChatMessages(id)).flatMap((delivery) => deliveryToChatMessage(delivery) || [])
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
      if (session.scanIds.length) {
        // Fetch every linked scan's SCO nodes at once instead of awaiting them
        // one after another — a session with N scans used to cost N serial
        // round-trips before its results deck filled in.
        const loaded = await Promise.all(
          session.scanIds.map(async (scanID) => {
            try {
              const nodes = await listSCONodes({ scanId: scanID, limit: 2000 })
              return { scanID, nodes }
            } catch {
              return { scanID, nodes: undefined as SCONode[] | undefined }
            }
          }),
        )
        // A session switch during scan loading bumps activationRef; discard
        // these stale results instead of writing them into the new session's
        // scanResults map.
        if (activation !== activationRef.current) return
        const withResult = loaded.filter((e) => e.nodes?.length)
        if (withResult.length) {
          setScanResults((prev) => {
            const next = new Map(prev)
            for (const e of withResult) next.set(e.scanID, e.nodes!)
            return next
          })
        }
      }
    } catch {}

    if (activation !== activationRef.current) return
    unsubRef.current = subscribeAOPEvents(
      id,
      handleAOPEvent,
      () => reconcileAfterReconnect(id),
    )
  }

  async function handleCreateSession(nodeURI: string) {
    try {
      const session = await createChatSession(nodeURI)
      setSelectedNodeURI(nodeURI)
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
	const lower = trimmed.toLowerCase()
	if (lower === '/clear') {
		try {
			const next = await resetChatSession(sessionID)
			await refreshSessions()
			const nextID = next.session?.id
			if (nextID) await activateSession(nextID, 'push')
		} catch (err: any) {
			setError(err.message || 'Failed to reset session')
		}
		return
	}
	if (lower === '/stop') {
		await handleCancelMessage()
		return
	}
	if (lower === '/exit' || lower === '/quit') {
		try {
			await closeChatSession(sessionID)
			await refreshSessions()
		} catch (err: any) {
			setError(err.message || 'Failed to close session')
		}
		return
	}
	const continueSession = lower === '/continue'
	let runContent = trimmed
	if (lower.startsWith('/followup ')) runContent = trimmed.slice(trimmed.indexOf(' ') + 1).trim()
	const command = !continueSession
		&& (runContent.startsWith('!') || (runContent.startsWith('/') && !runContent.startsWith('/skill:') && !lower.startsWith('/followup ')))
	if (command) {
		const msgID = safeUUID()
		const optimistic: ChatMessage = { id: msgID, session_id: sessionID, role: 'user', content: runContent, created_at: new Date().toISOString() }
		setMessages((prev) => [...prev, optimistic])
		appendTimeline({ id: msgID, kind: 'message', timestamp: Date.now(), message: optimistic })
		try {
			await executeChatCommand(sessionID, runContent)
		} catch (err: any) {
			setError(err.message || 'Failed to execute command')
		}
		return
	}

    const msgID = safeUUID()

	const optimistic: ChatMessage = {
      id: msgID,
      session_id: sessionID,
      role: 'user',
		content: runContent,
      created_at: new Date().toISOString(),
    }
	if (!continueSession) {
		setMessages((prev) => [...prev, optimistic])
		appendTimeline({ id: msgID, kind: 'message', timestamp: Date.now(), message: optimistic })
	}
    setError('')
    setPendingResponse(true)

    try {
		const sent = await sendChatMessage(sessionID, runContent, { ...opts, messageID: msgID, continueSession })
		activeTurnRef.current = sent.turnId || ''
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
    const connected = agents.find((a) => a.nodeUri === selectedNodeURI)
    const nodeURI = connected?.nodeUri || agents[0]?.nodeUri
    if (!nodeURI) {
      setError('No node connected — launch a local agent or connect one first.')
      return null
    }
    try {
      const session = await createChatSession(nodeURI)
      setSelectedNodeURI(nodeURI)
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
    nodeURI?: string,
    opts?: { activate?: boolean; skipRefresh?: boolean },
  ): Promise<AOPSession | null> {
    const connected = agents.find((a) => a.nodeUri === selectedNodeURI)
    const targetNodeURI = nodeURI || connected?.nodeUri || agents[0]?.nodeUri
    if (!targetNodeURI) {
      setError('No node connected — launch a local agent or connect one first.')
      return null
    }
    try {
      const session = await createChatSession(targetNodeURI, target)
      await sendChatMessage(session.id, prompt)
      if (!opts?.skipRefresh) await refreshSessions()
      if (opts?.activate) {
        setSelectedNodeURI(targetNodeURI)
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
    const connected = agents.find((a) => a.nodeUri === selectedNodeURI)
    const nodeURI = connected?.nodeUri || agents[0]?.nodeUri
    if (!nodeURI) {
      setError('No node connected — launch a local agent or connect one first.')
      return null
    }
    try {
      const session = await createChatSession(nodeURI, args.title, args.scanID)
      setSelectedNodeURI(nodeURI)
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
          quickDispatch(it.target, it.prompt, fleet[(i + j) % fleet.length].nodeUri, { skipRefresh: true }),
        ),
      )
    }
    await refreshSessions()
  }

  async function handleCancelMessage() {
    const sessionID = activeSessionRef.current
    if (!sessionID) return
    try {
		await cancelChatSession(sessionID, activeTurnRef.current)
		finalizeRun()
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
    selectedNodeURI,
    sessions,
    activeSessionID,
    timeline,
    aopEvents,
    scanResults,
    isThinking,
    busy: pendingResponse || isThinking,
    error,
    selectNode: (nodeURI: string) => {
      setSelectedNodeURI(nodeURI)
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
