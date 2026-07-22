import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { FitAddon } from '@xterm/addon-fit'
import { Terminal as XTerm } from '@xterm/xterm'
import { Info, Plus, RefreshCw, Square } from 'lucide-react'
import { agentTerminalWebSocketURL } from '../../api'
import type { AgentInfo } from '../../api'
import { Button, Tooltip, TooltipTrigger, TooltipContent } from '@cyber/ui'
import {
  type PTYSession,
  type TerminalStatus,
  activitySeq,
  compareSessionsByActivity,
  encodeTerminalData,
  mergeSession,
  parsePTYFrame,
  sessionFromFrame,
  sessionsFromFrame,
  sessionTitle,
  upsertSession,
  writeTerminalData,
} from '@cyber/terminal'
import { TerminalView, TerminalHeader, SessionNavigator, SessionButton, sessionDetails } from '@cyber/terminal'
import { TerminalDetails } from './TerminalDetails'

const REPL_NAME = 'main-repl'

interface AgentTerminalProps {
  agent: AgentInfo
}

export default function AgentTerminal({ agent }: AgentTerminalProps) {
  const { t } = useTranslation('agent')
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
  const desiredSessionIDRef = useRef('')
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
    term.reset()
    setStatus('connecting')
    setSessions([])
    setActiveID('')
    setUnreadIDs(new Set())
    activeRef.current = ''
    sessionsRef.current = []
    seenActivityRef.current = {}
    activityReadyRef.current = false
    desiredSessionIDRef.current = ''

    const ws = new WebSocket(agentTerminalWebSocketURL(agent.id))
    wsRef.current = ws
    const size = () => ({ cols: term.cols, rows: term.rows })
    const fitTerminal = () => {
      try { fit.fit() } catch {}
    }
    const sendTo = (message: Record<string, unknown>) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(message))
    }
    const requestDesiredSession = (knownSessions: PTYSession[] = sessionsRef.current) => {
      if (ws.readyState !== WebSocket.OPEN) return
      const desiredID = desiredSessionIDRef.current
      const desired = desiredID
        ? knownSessions.find((s) => s.id === desiredID && (!s.state || s.state === 'running'))
        : null
      fitTerminal()
      term.reset()
      if (desired?.id) {
        sendTo({ type: 'attach', session_id: desired.id, ...size() })
        return
      }
      desiredSessionIDRef.current = ''
      const repl = knownSessions.find((s) => s.state === 'running' && s.kind === 'repl' && (s.name === REPL_NAME || !s.name))
        || knownSessions.find((s) => s.state === 'running' && s.kind === 'repl')
      if (repl?.id) {
        sendTo({ type: 'attach', session_id: repl.id, ...size() })
      }
    }

    const dataDisposable = term.onData((data) => {
      if (!activeRef.current) return
      sendTo({ type: 'input', session_id: activeRef.current, data: encodeTerminalData(data) })
    })
    const resizeDisposable = term.onResize(({ cols, rows }) => {
      if (!activeRef.current) return
      sendTo({ type: 'resize', session_id: activeRef.current, cols, rows })
    })

    ws.onopen = () => {
      setStatus('connected')
      sendTo({ type: 'list' })
    }
    ws.onmessage = (event) => {
      const msg = parsePTYFrame(event.data)
      if (!msg) return
      switch (msg.type) {
        case 'sessions': {
          const next = sessionsFromFrame(msg)
          applySessions(next)
          if (!activeRef.current) requestDesiredSession(next)
          break
        }
        case 'opened':
        case 'attached': {
          const session = sessionFromFrame(msg)
          const id = msg.session_id || session?.id || ''
          if (session) rememberSession(session)
          if (id) {
            activeRef.current = id
            desiredSessionIDRef.current = id
            setActiveID(id)
            markSessionRead(id, session)
          }
          setStatus('connected')
          fitTerminal()
          if (id) sendTo({ type: 'resize', session_id: id, ...size() })
          sendTo({ type: 'list' })
          term.focus()
          break
        }
        case 'output': {
          const id = msg.session_id || ''
          if (id && activeRef.current && id !== activeRef.current) { markSessionUnread(id); break }
          writeTerminalData(term, msg)
          markSessionRead(id || activeRef.current)
          break
        }
        case 'closed': {
          const session = sessionFromFrame(msg)
          const id = msg.session_id || session?.id || ''
          const known = sessionsRef.current.find((s) => s.id === id) || null
          const current = session ? { ...known, ...session } : known
          if (session) rememberSession(session)
          if (id === activeRef.current) {
            markSessionRead(id, current)
            activeRef.current = ''
            setActiveID('')
            desiredSessionIDRef.current = ''
            requestDesiredSession()
          }
          sendTo({ type: 'list' })
          break
        }
        case 'detached':
          activeRef.current = ''
          setActiveID('')
          setStatus('connecting')
          break
        case 'error':
          if (/no such session/i.test(msg.error || '')) {
            desiredSessionIDRef.current = ''
            requestDesiredSession()
            break
          }
          setStatus('error')
          term.write(`\r\n[pty error] ${msg.error || 'unknown error'}\r\n`)
          break
      }
    }
    ws.onerror = () => setStatus('error')
    ws.onclose = () => setStatus((current) => current === 'error' ? current : 'closed')

    return () => {
      ws.onmessage = null
      ws.onclose = null
      ws.onerror = null
      ws.onopen = null
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'detach' }))
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
    desiredSessionIDRef.current = session.id
    termRef.current?.reset()
    activeRef.current = session.id
    setActiveID(session.id)
    markSessionRead(session.id, session)
    send({ type: 'attach', session_id: session.id, ...terminalSize() })
  }

  function attachRepl() {
    if (replSession) { attachSession(replSession); return }
    desiredSessionIDRef.current = ''
    send({ type: 'list' })
  }

  function openShell() {
    desiredSessionIDRef.current = ''
    termRef.current?.reset()
    activeRef.current = ''
    setActiveID('')
    send({ type: 'detach' })
    send({ type: 'open', kind: 'shell', name: `shell-${agent.name}`, ...terminalSize() })
  }

  function stopActiveSession() {
    if (!activeID || activeSession?.kind === 'repl') return
    send({ type: 'kill', session_id: activeID })
  }

  const activeTitle = activeSession ? sessionTitle(activeSession) : activeID
  const canStopActive = activeSession?.kind !== 'repl' && activeSession?.state === 'running'
  const detailsSession = activeSession || replSession
  const summaryText = taskSummary.updates
    ? `${t('summaryRunning', { count: taskSummary.running })} · ${t('summaryNew', { count: taskSummary.updates })}`
    : t('summaryRunning', { count: taskSummary.running })

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <TerminalHeader
        status={status}
        title={activeTitle || t('console')}
        actions={
          <>
            <IconButton label={t('newShellPty')} onClick={openShell}><Plus className="h-3.5 w-3.5" /></IconButton>
            <IconButton label={t('refreshSessions')} onClick={() => send({ type: 'list' })}><RefreshCw className="h-3.5 w-3.5" /></IconButton>
            <IconButton label={t('stopActiveTask')} onClick={stopActiveSession} disabled={!canStopActive}><Square className="h-3.5 w-3.5" /></IconButton>
            <IconButton label={detailsOpen ? t('hideDetails') : t('showDetails')} onClick={() => setDetailsOpen((v) => !v)} active={detailsOpen}><Info className="h-3.5 w-3.5" /></IconButton>
          </>
        }
      />
      <div className="flex min-h-0 min-w-0 flex-1 flex-col lg:flex-row">
        <SessionNavigator
          activeID={activeID}
          sessions={taskSessions}
          unreadIDs={unreadIDs}
          onSelect={attachSession}
          listLabel={t('tasks')}
          summary={summaryText}
          emptyText={t('noTasksYet')}
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
  const { t } = useTranslation('agent')
  return (
    <SessionButton
      active={active}
      title={t('mainRepl')}
      meta={replSession ? t('alwaysOn') : t('starting')}
      state={replSession?.state || 'running'}
      details={replSession ? sessionDetails(replSession) : t('mainReplStarting')}
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
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          active={active}
          aria-label={label}
          title={label}
          disabled={disabled}
          onClick={onClick}
          className="text-muted-foreground"
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent side="bottom">{label}</TooltipContent>
    </Tooltip>
  )
}
