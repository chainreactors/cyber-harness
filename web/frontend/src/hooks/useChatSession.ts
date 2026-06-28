import { useCallback, useEffect, useRef, useState } from 'react'
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

export type TimelineItemKind = 'message' | 'assistant_response' | 'tool_call' | 'scan_started' | 'scan_progress' | 'scan_complete' | 'thinking' | 'agent_joined'

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

export function useChatSession() {
  const [agents, setAgents] = useState<AgentInfo[]>([])
  const [selectedAgentID, setSelectedAgentID] = useState<string | null>(null)
  const [sessions, setSessions] = useState<ChatSession[]>([])
  const [activeSessionID, setActiveSessionID] = useState<string | null>(null)
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [timeline, setTimeline] = useState<TimelineItem[]>([])
  const timelineRef = useRef<TimelineItem[]>([])
  const [streamingText, setStreamingText] = useState<string | null>(null)
  const [streamingAgent, setStreamingAgent] = useState<string | null>(null)
  const [scanResults, setScanResults] = useState<Map<string, ScanResult>>(() => new Map())
  const [detailScanID, setDetailScanID] = useState<string | null>(null)
  const [isThinking, setIsThinking] = useState(false)
  const [pendingResponse, setPendingResponse] = useState(false)
  const [error, setError] = useState('')
  const unsubRef = useRef<(() => void) | null>(null)
  const activationRef = useRef(0)
  const userMsgIdsRef = useRef<Set<string>>(new Set())
  const scanLinesRef = useRef<Map<string, string[]>>(new Map())
  const activeTurnRef = useRef<string | null>(null)
  const activeSessionRef = useRef<string | null>(null)

  useEffect(() => {
    activeSessionRef.current = activeSessionID
  }, [activeSessionID])

  const refreshAgents = useCallback(async () => {
    try {
      const list = await listAgents()
      setAgents(list)
      setSelectedAgentID((current) =>
        current && list.some((a) => a.id === current) ? current : list[0]?.id || null,
      )
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
    const interval = setInterval(() => refreshAgents(), 5000)
    return () => clearInterval(interval)
  }, [refreshAgents, refreshSessions])

  function closeSubscription() {
    if (unsubRef.current) {
      unsubRef.current()
      unsubRef.current = null
    }
  }

  function resetSessionState() {
    setMessages([])
    timelineRef.current = []
    setTimeline([])
    setStreamingText(null)
    setStreamingAgent(null)
    setScanResults(new Map())
    setDetailScanID(null)
    setIsThinking(false)
    setPendingResponse(false)
    setError('')
    userMsgIdsRef.current = new Set()
    scanLinesRef.current = new Map()
    activeTurnRef.current = null
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
      const scope = [event.session_id || activeSessionID || 'session', event.agent_id || event.agent_name || 'agent', event.turn].join('-')
      const id = `turn-${scope}`
      activeTurnRef.current = id
      return id
    }
    if (activeTurnRef.current) return activeTurnRef.current
    const id = `turn-${timestamp}-${crypto.randomUUID()}`
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
    const msg: ChatMessage = {
      id: event.message_id || `assistant-response-${responseID}`,
      session_id: event.session_id,
      role: 'assistant',
      agent_id: event.agent_id,
      agent_name: event.agent_name,
      content,
      created_at: new Date().toISOString(),
    }
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

  function handleChatEvent(event: ChatEvent) {
    const now = Date.now()

    switch (event.type) {
      case 'message': {
        if (!event.content) break
        const isUserEcho = event.message_id && userMsgIdsRef.current.has(event.message_id)
        if (isUserEcho) break
        if ((event.role || 'assistant') === 'assistant') {
          if (!activeTurnRef.current && isDuplicateAssistantResponse(event.content)) {
            setIsThinking(false)
            setPendingResponse(false)
            setStreamingText(null)
            setStreamingAgent(null)
            break
          }
          setAssistantResponseMessage(event, event.content, false, now)
          setIsThinking(false)
          setPendingResponse(false)
          setStreamingText(null)
          setStreamingAgent(null)
          break
        }
        const msg: ChatMessage = {
          id: event.message_id || crypto.randomUUID(),
          session_id: event.session_id,
          role: event.role || 'assistant',
          agent_id: event.agent_id,
          agent_name: event.agent_name,
          content: event.content,
          created_at: new Date().toISOString(),
        }
        appendMessage(msg, now)
        setIsThinking(false)
        setPendingResponse(false)
        break
      }

      case 'message_start':
        setAssistantResponseMessage(event, event.content || '', true, now)
        setStreamingText(null)
        setStreamingAgent(event.agent_name || null)
        setIsThinking(false)
        break

      case 'message_delta':
        if (event.content !== undefined) {
          setAssistantResponseMessage(event, event.content, true, now)
        } else if (event.delta) {
          upsertAssistantResponse((response) => {
            const prevContent = response.response?.content || ''
            const msg: ChatMessage = response.response || {
              id: `assistant-response-${response.id}`,
              session_id: event.session_id,
              role: 'assistant',
              agent_id: event.agent_id,
              agent_name: event.agent_name,
              content: '',
              created_at: new Date().toISOString(),
            }
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
        setStreamingText(null)
        setStreamingAgent(null)
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
        const tcID = event.tool_call_id || crypto.randomUUID()
        const tc: ToolCallState = {
          id: tcID,
          toolName: event.tool_name || '',
          toolArgs: event.tool_args || '',
          pending: true,
        }
        upsertAssistantTool(event, tc, now)
        setIsThinking(false)
        setStreamingText(null)
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
        setStreamingAgent(event.agent_name || null)
        if (event.content || event.data || event.delta) {
          setAssistantThinking(event, event.content || event.data || event.delta || '', now)
        } else {
          upsertAssistantResponse((response) => ({ ...response, streaming: true }), event, now)
        }
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
          setDetailScanID((current) => current || event.scan_id!)
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
        setStreamingAgent(event.agent_name || null)
        break

      case 'error':
        if (event.error) setError(event.error)
        setPendingResponse(false)
        break
    }
  }

  function buildTimelineFromMessages(msgs: ChatMessage[]): TimelineItem[] {
    const built: TimelineItem[] = []
    const responsesByTurn = new Map<string, TimelineItem>()
    const toolsByID = new Map<string, ToolCallState>()
    let currentResponse: TimelineItem | null = null
    let pendingAgentName: string | undefined

    function turnKey(msg: ChatMessage, turn: number): string {
      if (!turn) return ''
      return [
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

    function toolMapKey(key: string, toolID: string): string {
      return key ? `${key}:${toolID}` : toolID
    }

    for (const msg of msgs) {
      const timestamp = new Date(msg.created_at).getTime()
      const eventType = metadataString(msg.metadata, 'event_type')
      const turn = metadataNumber(msg.metadata, 'turn')
      const key = turnKey(msg, turn)

      if (msg.role === 'tool_call') {
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
        const response = ensureResponse(timestamp, msg.agent_name, turn, key)
        response.assistantResponse!.thinking = content
        pendingAgentName = undefined
        continue
      }

      if (eventType === 'agent_joined') {
        pendingAgentName = msg.agent_name || msg.content.replace(/\s+joined$/, '')
        continue
      }

      if (msg.role === 'assistant') {
        const response = ensureResponse(timestamp, msg.agent_name, turn, key)
        response.assistantResponse!.response = msg
        response.assistantResponse!.streaming = false
        currentResponse = null
        pendingAgentName = undefined
        continue
      }

      if (msg.role === 'user') {
        currentResponse = null
        pendingAgentName = undefined
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

  async function activateSession(id: string, route: RouteMode) {
    const activation = ++activationRef.current
    closeSubscription()
    resetSessionState()
    setActiveSessionID(id)
    setSessionRoute(id, route)

    try {
      const msgs = await listChatMessages(id)
      if (activation !== activationRef.current) return
      setMessages(msgs)
      const builtTimeline = buildTimelineFromMessages(msgs)
      timelineRef.current = builtTimeline
      setTimeline(builtTimeline)

      const session = await getChatSession(id)
      if (activation !== activationRef.current) return
      if (session.scan_ids) {
        for (const scanID of session.scan_ids) {
          try {
            const scan = await getScan(scanID)
            if (scan.result) {
              setScanResults((prev) => {
                const next = new Map(prev)
                next.set(scanID, scan.result!)
                return next
              })
              setDetailScanID((current) => current || scanID)
            }
          } catch {}
        }
      }
    } catch {}

    if (activation !== activationRef.current) return
    unsubRef.current = subscribeChatEvents(id, handleChatEvent)
  }

  async function handleCreateSession(agentID: string) {
    try {
      const session = await createChatSession(agentID)
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

  async function handleSendMessage(content: string) {
    const sessionID = activeSessionRef.current
    if (!sessionID) return
    const trimmed = content.trim()
    if (!trimmed) return

    const msgID = crypto.randomUUID()
    userMsgIdsRef.current.add(msgID)
    activeTurnRef.current = null

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
      const serverMsg = await sendChatMessage(sessionID, trimmed)
      userMsgIdsRef.current.add(serverMsg.id)
      await refreshSessions()
    } catch (err: any) {
      setPendingResponse(false)
      setError(err.message || 'Failed to send message')
    }
  }

  async function handleCancelMessage() {
    const sessionID = activeSessionRef.current
    if (!sessionID) return
    setTimelineItems((prev) => prev.map((item) => (
      item.kind === 'assistant_response' && item.assistantResponse
        ? { ...item, assistantResponse: { ...item.assistantResponse, streaming: false } }
        : item
    )))
    setIsThinking(false)
    setPendingResponse(false)
    setStreamingText(null)
    setStreamingAgent(null)
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
        // Scan routes are owned by useScanSession/App view switching.
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

  return {
    agents,
    selectedAgentID,
    sessions,
    activeSessionID,
    messages,
    timeline,
    streamingText,
    streamingAgent,
    scanResults,
    detailScanID,
    isThinking,
    busy: pendingResponse || isThinking || streamingText !== null || timeline.some((item) => (
      item.kind === 'assistant_response' && item.assistantResponse?.streaming
    )),
    error,
    selectAgent: (id: string) => setSelectedAgentID(id),
    createSession: handleCreateSession,
    selectSession: (id: string) => activateSession(id, 'push'),
    deleteSession: handleDeleteSession,
    sendMessage: handleSendMessage,
    cancelMessage: handleCancelMessage,
    showScanDetail: (scanID: string) => setDetailScanID(scanID),
    hideDetail: () => setDetailScanID(null),
    clearError: () => setError(''),
    refreshSessions,
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
