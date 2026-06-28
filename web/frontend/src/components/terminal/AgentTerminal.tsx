import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { FitAddon } from '@xterm/addon-fit'
import { Terminal as XTerm } from '@xterm/xterm'
import { Info, Plus, RefreshCw, Square } from 'lucide-react'
import { agentTerminalWebSocketURL } from '../../api'
import type { AgentInfo } from '../../api'
import { cn } from '@aspect/theme'
import { Tooltip, TooltipTrigger, TooltipContent } from '@aspect/ui'
import {
  type PTYSession,
  type TerminalStatus,
  activitySeq,
  compareSessionsByActivity,
  mergeSession,
  parseTerminalMessage,
  sessionFromPayload,
  sessionPayload,
  sessionTitle,
  stringPayload,
  upsertSession,
  writeTerminalData,
} from '@aspect/terminal'
import { TerminalView, TerminalHeader, SessionNavigator, SessionButton, sessionDetails } from '@aspect/terminal'
import { TerminalDetails } from './TerminalDetails'

const REPL_NAME = 'main-repl'

interface AgentTerminalProps {
  agent: AgentInfo
}

export default function AgentTerminal({ agent }: AgentTerminalProps) {
  const [status, setStatus] = useState<TerminalStatus>('connecting')
  const [sessions, setSessions] = useState<PTYSession[]>([])
  const [activeID, setActiveID] = useState('')
  const [unreadIDs, setUnreadIDs] = useState<Set<string>>(() => new Set())
  const [detailsOpen, setDetailsOpen] = useState(false)
  const activeRef = useRef('')
  const sessionsRef = useRef<PTYSession[]>([])
  const seenActivityRef = useRef<Record<string, number>>({})
  const activityReadyRef = useRef(false)
  const wsRef = useRef<WebSocket | null>(null)
  const cleanupRef = useRef<(() => void) | null>(null)
  const termRef = useRef<XTerm | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const [terminalReadySeq, setTerminalReadySeq] = useState(0)

  const replSession = useMemo(() => {
    return sessions.find((s) => s.kind === 'repl' && (s.name === REPL_NAME || !s.name))
      || sessions.find((s) => s.kind === 'repl')
      || null
  }, [sessions])

  const taskSessions = useMemo(() => {
    return sessions.filter((s) => s.kind !== 'repl').slice().sort(compareSessionsByActivity)
  }, [sessions])

  const taskSummary = useMemo(() => {
    let running = 0
    let updates = 0
    for (const s of taskSessions) {
      if (s.state === 'running') running += 1
      if (s.id !== activeID && unreadIDs.has(s.id)) updates += 1
    }
    return { running, updates }
  }, [activeID, taskSessions, unreadIDs])

  const activeSession = useMemo(() => sessions.find((s) => s.id === activeID) || null, [activeID, sessions])

  useEffect(() => { activeRef.current = activeID }, [activeID])
  useEffect(() => { sessionsRef.current = sessions }, [sessions])

  const handleTerminalReady = useCallback((term: XTerm, fit: FitAddon) => {
    termRef.current = term
    fitRef.current = fit
    setTerminalReadySeq((seq) => seq + 1)
  }, [])

  function connectWebSocket(term: XTerm, fit: FitAddon) {
    setStatus('connecting')
    setSessions([])
    setActiveID('')
    setUnreadIDs(new Set())
    activeRef.current = ''
    sessionsRef.current = []
    seenActivityRef.current = {}
    activityReadyRef.current = false

    const ws = new WebSocket(agentTerminalWebSocketURL(agent.id))
    wsRef.current = ws
    const send = (message: Record<string, unknown>) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(message))
    }
    const size = () => ({ cols: term.cols, rows: term.rows })

    const dataDisposable = term.onData((data) => {
      if (!activeRef.current) return
      send({ type: 'pty.input', payload: { session_id: activeRef.current, data } })
    })
    const resizeDisposable = term.onResize(({ cols, rows }) => {
      if (!activeRef.current) return
      send({ type: 'pty.resize', payload: { session_id: activeRef.current, cols, rows } })
    })

    ws.onopen = () => {
      setStatus('connected')
      send({ type: 'pty.open', payload: { kind: 'repl', name: REPL_NAME, singleton: true, ...size() } })
      send({ type: 'pty.list' })
    }
    ws.onmessage = (event) => {
      const msg = parseTerminalMessage(event.data)
      if (!msg) return
      switch (msg.type) {
        case 'pty.sessions':
          applySessions(sessionPayload(msg))
          break
        case 'pty.opened':
        case 'pty.attached': {
          const id = stringPayload(msg, 'session_id')
          const session = sessionFromPayload(msg)
          if (session) rememberSession(session)
          if (id) { activeRef.current = id; setActiveID(id); markSessionRead(id, session) }
          setStatus('connected')
          send({ type: 'pty.list' })
          term.focus()
          break
        }
        case 'pty.output': {
          const id = stringPayload(msg, 'session_id')
          if (id && activeRef.current && id !== activeRef.current) { markSessionUnread(id); break }
          writeTerminalData(term, msg)
          markSessionRead(id || activeRef.current)
          break
        }
        case 'pty.closed': {
          const id = stringPayload(msg, 'session_id')
          const session = sessionFromPayload(msg)
          const known = sessionsRef.current.find((s) => s.id === id) || null
          const current = session ? { ...known, ...session } : known
          if (session) rememberSession(session)
          if (id === activeRef.current) {
            markSessionRead(id, current)
            if (current?.kind === 'repl') {
              setStatus('connected')
              term.reset()
              send({ type: 'pty.open', payload: { kind: 'repl', name: REPL_NAME, singleton: true, ...size() } })
              send({ type: 'pty.list' })
              break
            }
            setStatus('closed')
            term.write('\r\n[session closed]\r\n')
          }
          send({ type: 'pty.list' })
          break
        }
        case 'pty.detached':
          activeRef.current = ''
          setActiveID('')
          break
        case 'pty.error':
          setStatus('error')
          term.write(`\r\n[pty error] ${msg.data || 'unknown error'}\r\n`)
          break
      }
    }
    ws.onerror = () => setStatus('error')
    ws.onclose = () => setStatus((c) => (c === 'error' ? c : 'closed'))

    return () => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'pty.detach' }))
      ws.close()
      resizeDisposable.dispose()
      dataDisposable.dispose()
      if (wsRef.current === ws) wsRef.current = null
    }
  }

  useEffect(() => {
    if (terminalReadySeq === 0) return
    const term = termRef.current
    const fit = fitRef.current
    if (!term || !fit) return

    cleanupRef.current?.()
    const cleanup = connectWebSocket(term, fit)
    cleanupRef.current = cleanup

    return () => {
      if (cleanupRef.current === cleanup) {
        cleanupRef.current = null
        cleanup()
      }
    }
  }, [agent.id, terminalReadySeq])

  function send(message: Record<string, unknown>) {
    if (wsRef.current?.readyState === WebSocket.OPEN) wsRef.current.send(JSON.stringify(message))
  }

  function terminalSize() {
    const term = termRef.current
    return term ? { cols: term.cols, rows: term.rows } : { cols: 80, rows: 24 }
  }

  function applySessions(next: PTYSession[]) {
    sessionsRef.current = next
    setSessions(next)
    setUnreadIDs((current) => {
      const unread = new Set(current)
      const ids = new Set(next.map((s) => s.id))
      for (const id of unread) { if (!ids.has(id)) unread.delete(id) }
      for (const s of next) {
        const seq = activitySeq(s)
        const seen = seenActivityRef.current[s.id]
        if (!activityReadyRef.current) { seenActivityRef.current[s.id] = seq; unread.delete(s.id); continue }
        if (s.id === activeRef.current) { seenActivityRef.current[s.id] = seq; unread.delete(s.id); continue }
        if (seen === undefined) { seenActivityRef.current[s.id] = seq; if (seq > 0) unread.add(s.id); continue }
        if (seq > seen) { seenActivityRef.current[s.id] = seq; unread.add(s.id) }
      }
      activityReadyRef.current = true
      return unread
    })
  }

  function markSessionRead(id: string, session?: PTYSession | null) {
    if (!id) return
    const c = session || sessionsRef.current.find((s) => s.id === id)
    if (c) seenActivityRef.current[id] = activitySeq(c)
    setUnreadIDs((items) => { if (!items.has(id)) return items; const next = new Set(items); next.delete(id); return next })
  }

  function markSessionUnread(id: string) {
    if (!id) return
    setUnreadIDs((items) => { if (items.has(id)) return items; const next = new Set(items); next.add(id); return next })
  }

  function rememberSession(session: PTYSession) {
    sessionsRef.current = mergeSession(sessionsRef.current, session)
    upsertSession(setSessions, session)
  }

  function attachSession(session: PTYSession) {
    if (!session.id) return
    termRef.current?.reset()
    activeRef.current = session.id
    setActiveID(session.id)
    markSessionRead(session.id, session)
    send({ type: 'pty.attach', payload: { session_id: session.id, ...terminalSize() } })
  }

  function attachRepl() {
    if (replSession) { attachSession(replSession); return }
    termRef.current?.reset()
    send({ type: 'pty.open', payload: { kind: 'repl', name: REPL_NAME, singleton: true, ...terminalSize() } })
  }

  function openShell() {
    termRef.current?.reset()
    activeRef.current = ''
    setActiveID('')
    send({ type: 'pty.detach' })
    send({ type: 'pty.open', payload: { kind: 'shell', name: `shell-${agent.name}`, ...terminalSize() } })
  }

  function stopActiveSession() {
    if (!activeID || activeSession?.kind === 'repl') return
    send({ type: 'pty.kill', payload: { session_id: activeID } })
  }

  const activeTitle = activeSession ? sessionTitle(activeSession) : activeID
  const canStopActive = activeSession?.kind !== 'repl' && activeSession?.state === 'running'
  const detailsSession = activeSession || replSession
  const summaryText = `${taskSummary.running} running${taskSummary.updates ? ` · ${taskSummary.updates} new` : ''}`

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <TerminalHeader
        status={status}
        title={activeTitle || 'Console'}
        actions={
          <>
            <IconButton label="New shell PTY" onClick={openShell}><Plus className="h-3.5 w-3.5" /></IconButton>
            <IconButton label="Refresh sessions" onClick={() => send({ type: 'pty.list' })}><RefreshCw className="h-3.5 w-3.5" /></IconButton>
            <IconButton label="Stop active task" onClick={stopActiveSession} disabled={!canStopActive}><Square className="h-3.5 w-3.5" /></IconButton>
            <IconButton label={detailsOpen ? 'Hide details' : 'Show details'} onClick={() => setDetailsOpen((v) => !v)} active={detailsOpen}><Info className="h-3.5 w-3.5" /></IconButton>
          </>
        }
      />
      <div className="flex min-h-0 min-w-0 flex-1 flex-col lg:flex-row">
        <SessionNavigator
          activeID={activeID}
          sessions={taskSessions}
          unreadIDs={unreadIDs}
          onSelect={attachSession}
          listLabel="Tasks"
          summary={summaryText}
          emptyText="No tasks yet"
          header={
            <SessionNavigatorReplButton
              active={!!replSession && replSession.id === activeID}
              replSession={replSession}
              unread={replSession ? replSession.id !== activeID && unreadIDs.has(replSession.id) : false}
              onClick={attachRepl}
            />
          }
        />
        <section className="flex min-h-0 min-w-0 flex-1 flex-col">
          <TerminalView onReady={handleTerminalReady} />
        </section>
        {detailsOpen && (
          <TerminalDetails
            agent={agent}
            session={detailsSession}
            status={status}
            taskSessions={taskSessions}
            onClose={() => setDetailsOpen(false)}
          />
        )}
      </div>
    </div>
  )
}

function SessionNavigatorReplButton({ active, replSession, unread, onClick }: {
  active: boolean; replSession: PTYSession | null; unread: boolean; onClick: () => void
}) {
  return (
    <SessionButton
      active={active}
      title="Main REPL"
      meta={replSession ? 'always on' : 'starting'}
      state={replSession?.state || 'running'}
      details={replSession ? sessionDetails(replSession) : 'Main REPL is starting'}
      unread={unread}
      onClick={onClick}
    />
  )
}

function IconButton({ children, active, disabled, label, onClick }: {
  children: ReactNode; active?: boolean; disabled?: boolean; label: string; onClick: () => void
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label={label}
          title={label}
          disabled={disabled}
          onClick={onClick}
          className={cn(
            'inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-40',
            active && 'bg-primary/10 text-primary',
          )}
        >
          {children}
        </button>
      </TooltipTrigger>
      <TooltipContent side="bottom">{label}</TooltipContent>
    </Tooltip>
  )
}
