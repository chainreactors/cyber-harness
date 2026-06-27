import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { FitAddon } from '@xterm/addon-fit'
import { Terminal as XTerm } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { Info, Plus, RefreshCw, Square, Terminal as TerminalIcon } from 'lucide-react'
import { agentTerminalWebSocketURL } from '../../api'
import type { AgentInfo } from '../../api'
import { cn } from '@aspect/theme'
import { Tooltip, TooltipTrigger, TooltipContent } from '@aspect/ui'
import { SessionNavigator } from './SessionNavigator'
import { TerminalDetails } from './TerminalDetails'
import type { PTYSession, TerminalStatus } from './terminal-utils'
import {
  REPL_NAME,
  activitySeq,
  compareTaskSessions,
  createTerminal,
  parseTerminalMessage,
  sessionFromPayload,
  sessionPayload,
  sessionTitle,
  stringPayload,
  terminalStatusColor,
  upsertSession,
  writeTerminalData,
} from './terminal-utils'

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
  const seenActivityRef = useRef<Record<string, number>>({})
  const activityReadyRef = useRef(false)
  const wsRef = useRef<WebSocket | null>(null)
  const termRef = useRef<XTerm | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const mountRef = useRef<HTMLDivElement | null>(null)

  const replSession = useMemo(() => {
    return sessions.find((session) => session.kind === 'repl' && (session.name === REPL_NAME || !session.name))
      || sessions.find((session) => session.kind === 'repl')
      || null
  }, [sessions])

  const taskSessions = useMemo(() => {
    return sessions
      .filter((session) => session.kind !== 'repl')
      .slice()
      .sort(compareTaskSessions)
  }, [sessions])

  const taskSummary = useMemo(() => {
    let running = 0
    let updates = 0
    for (const session of taskSessions) {
      if (session.state === 'running') running += 1
      if (session.id !== activeID && unreadIDs.has(session.id)) updates += 1
    }
    return { running, updates }
  }, [activeID, taskSessions, unreadIDs])

  const activeSession = useMemo(() => {
    return sessions.find((session) => session.id === activeID) || null
  }, [activeID, sessions])

  useEffect(() => {
    activeRef.current = activeID
  }, [activeID])

  useEffect(() => {
    const mount = mountRef.current
    if (!mount) return

    setStatus('connecting')
    setSessions([])
    setActiveID('')
    setUnreadIDs(new Set())
    activeRef.current = ''
    seenActivityRef.current = {}
    activityReadyRef.current = false

    const term = createTerminal()
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(mount)
    fit.fit()
    term.focus()
    termRef.current = term
    fitRef.current = fit

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
    const resizeObserver = new ResizeObserver(() => fit.fit())
    resizeObserver.observe(mount)

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
          if (session) upsertSession(setSessions, session)
          if (id) {
            activeRef.current = id
            setActiveID(id)
            markSessionRead(id, session)
          }
          setStatus('connected')
          send({ type: 'pty.list' })
          term.focus()
          break
        }
        case 'pty.output':
          writeTerminalData(term, msg)
          markSessionRead(activeRef.current)
          break
        case 'pty.closed': {
          const id = stringPayload(msg, 'session_id')
          const session = sessionFromPayload(msg)
          if (session) upsertSession(setSessions, session)
          if (id === activeRef.current) {
            markSessionRead(id, session)
            if (session?.kind === 'repl') {
              setStatus('connected')
              resetTerminal()
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
    ws.onclose = () => setStatus((current) => (current === 'error' ? current : 'closed'))

    return () => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'pty.detach' }))
      ws.close()
      resizeObserver.disconnect()
      resizeDisposable.dispose()
      dataDisposable.dispose()
      term.dispose()
      wsRef.current = null
      termRef.current = null
      fitRef.current = null
    }
  }, [agent.id])

  function send(message: Record<string, unknown>) {
    if (wsRef.current?.readyState === WebSocket.OPEN) wsRef.current.send(JSON.stringify(message))
  }

  function terminalSize() {
    const term = termRef.current
    return term ? { cols: term.cols, rows: term.rows } : { cols: 80, rows: 24 }
  }

  function resetTerminal() {
    termRef.current?.reset()
  }

  function refreshSessions() {
    send({ type: 'pty.list' })
  }

  function applySessions(next: PTYSession[]) {
    setSessions(next)
    setUnreadIDs((current) => {
      const unread = new Set(current)
      const ids = new Set(next.map((session) => session.id))
      for (const id of unread) {
        if (!ids.has(id)) unread.delete(id)
      }
      for (const session of next) {
        const seq = activitySeq(session)
        const seen = seenActivityRef.current[session.id]
        if (!activityReadyRef.current) {
          seenActivityRef.current[session.id] = seq
          unread.delete(session.id)
          continue
        }
        if (session.id === activeRef.current) {
          seenActivityRef.current[session.id] = seq
          unread.delete(session.id)
          continue
        }
        if (seen === undefined) {
          seenActivityRef.current[session.id] = seq
          if (seq > 0) unread.add(session.id)
          continue
        }
        if (seq > seen) {
          seenActivityRef.current[session.id] = seq
          unread.add(session.id)
        }
      }
      activityReadyRef.current = true
      return unread
    })
  }

  function markSessionRead(id: string, session?: PTYSession | null) {
    if (!id) return
    const current = session || sessions.find((item) => item.id === id)
    if (current) seenActivityRef.current[id] = activitySeq(current)
    setUnreadIDs((items) => {
      if (!items.has(id)) return items
      const next = new Set(items)
      next.delete(id)
      return next
    })
  }

  function attachRepl() {
    if (replSession) {
      attachSession(replSession)
      return
    }
    resetTerminal()
    send({ type: 'pty.open', payload: { kind: 'repl', name: REPL_NAME, singleton: true, ...terminalSize() } })
  }

  function openShell() {
    resetTerminal()
    activeRef.current = ''
    setActiveID('')
    send({ type: 'pty.detach' })
    send({ type: 'pty.open', payload: { kind: 'shell', name: `shell-${agent.name}`, ...terminalSize() } })
  }

  function attachSession(session: PTYSession) {
    if (!session.id) return
    resetTerminal()
    activeRef.current = session.id
    setActiveID(session.id)
    markSessionRead(session.id, session)
    send({ type: 'pty.attach', payload: { session_id: session.id, ...terminalSize() } })
  }

  function stopActiveSession() {
    if (!activeID || activeSession?.kind === 'repl') return
    send({ type: 'pty.kill', payload: { session_id: activeID } })
  }

  const activeTitle = activeSession ? sessionTitle(activeSession) : activeID
  const canStopActive = activeSession?.kind !== 'repl' && activeSession?.state === 'running'
  const detailsSession = activeSession || replSession

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <TerminalHeader
        status={status}
        title={activeTitle || 'Console'}
        actions={
          <>
            <IconButton label="New shell PTY" onClick={openShell}>
              <Plus className="h-3.5 w-3.5" />
            </IconButton>
            <IconButton label="Refresh sessions" onClick={refreshSessions}>
              <RefreshCw className="h-3.5 w-3.5" />
            </IconButton>
            <IconButton label="Stop active task" onClick={stopActiveSession} disabled={!canStopActive}>
              <Square className="h-3.5 w-3.5" />
            </IconButton>
            <IconButton
              label={detailsOpen ? 'Hide details' : 'Show details'}
              onClick={() => setDetailsOpen((value) => !value)}
              active={detailsOpen}
            >
              <Info className="h-3.5 w-3.5" />
            </IconButton>
          </>
        }
      />
      <div className="flex min-h-0 min-w-0 flex-1 flex-col lg:flex-row">
        <SessionNavigator
          activeID={activeID}
          replSession={replSession}
          summary={taskSummary}
          taskSessions={taskSessions}
          unreadIDs={unreadIDs}
          onAttachRepl={attachRepl}
          onAttach={attachSession}
        />
        <section className="flex min-h-0 min-w-0 flex-1 flex-col">
          <div ref={mountRef} className="min-h-0 min-w-0 flex-1 bg-[#060a0d] p-2" />
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

function TerminalHeader({ actions, status, title }: { actions?: ReactNode; status: TerminalStatus; title: string }) {
  return (
    <div className="flex h-11 min-w-0 shrink-0 items-center justify-between border-b border-border px-3">
      <div className="flex min-w-0 items-center gap-2" title={title}>
        <TerminalIcon className="h-4 w-4 shrink-0 text-cyber-400" />
        <span className="truncate text-sm font-medium text-foreground">{title}</span>
        <span className={cn('shrink-0 rounded px-1.5 py-0.5 text-[10px]', terminalStatusColor(status))}>
          {status}
        </span>
      </div>
      {actions && <div className="flex items-center gap-1">{actions}</div>}
    </div>
  )
}

function IconButton({
  children,
  active,
  disabled,
  label,
  onClick,
}: {
  children: ReactNode
  active?: boolean
  disabled?: boolean
  label: string
  onClick: () => void
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
            active && 'bg-cyber-400/10 text-cyber-700 dark:text-cyber-300',
          )}
        >
          {children}
        </button>
      </TooltipTrigger>
      <TooltipContent side="bottom">{label}</TooltipContent>
    </Tooltip>
  )
}
