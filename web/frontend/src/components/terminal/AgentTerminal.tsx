import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { create } from '@bufbuild/protobuf'
import { FitAddon } from '@xterm/addon-fit'
import { Terminal as XTerm } from '@xterm/xterm'
import { Info, Plus, RefreshCw, Square } from 'lucide-react'
import { PtyProtocolMessageSchema, type PtyProtocolMessage } from '@cyber/aop'
import { aopClient, type AgentView } from '../../api'
import { Button, Tooltip, TooltipTrigger, TooltipContent } from '@cyber/ui'
import {
  type PTYSession,
  type TerminalStatus,
  activitySeq,
  compareSessionsByActivity,
  encodeTerminalData,
  mergeSession,
  sessionFromFrame,
  sessionsFromFrame,
  sessionTitle,
  upsertSession,
  writeTerminalData,
  TerminalView,
  TerminalHeader,
  SessionNavigator,
  SessionButton,
  sessionDetails,
} from '@cyber/terminal'
import { TerminalDetails } from './TerminalDetails'

const REPL_NAME = 'main-repl'

export default function AgentTerminal({ agent }: { agent: AgentView }) {
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
  const streamIDRef = useRef('')
  const cleanupRef = useRef<(() => void) | null>(null)
  const termRef = useRef<XTerm | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const [terminalReadySeq, setTerminalReadySeq] = useState(0)

  const replSession = useMemo(() => sessions.find((s) => s.kind === 'repl' && (s.name === REPL_NAME || !s.name)) || sessions.find((s) => s.kind === 'repl') || null, [sessions])
  const taskSessions = useMemo(() => sessions.filter((s) => s.kind !== 'repl').slice().sort(compareSessionsByActivity), [sessions])
  const activeSession = useMemo(() => sessions.find((s) => s.id === activeID) || null, [activeID, sessions])
  const taskSummary = useMemo(() => ({ running: taskSessions.filter((s) => s.state === 'running').length, updates: taskSessions.filter((s) => s.id !== activeID && unreadIDs.has(s.id)).length }), [activeID, taskSessions, unreadIDs])

  useEffect(() => { activeRef.current = activeID }, [activeID])
  useEffect(() => { sessionsRef.current = sessions }, [sessions])

  const handleTerminalReady = useCallback((term: XTerm, fit: FitAddon) => {
    termRef.current = term
    fitRef.current = fit
    setTerminalReadySeq((seq) => seq + 1)
  }, [])

  const sendFrame = useCallback((message: PtyProtocolMessage) => {
    aopClient.send(PtyProtocolMessageSchema, message)
  }, [])

  const sendList = useCallback(() => {
    const streamId = streamIDRef.current
    if (!streamId) return
    sendFrame(create(PtyProtocolMessageSchema, { message: { case: 'list', value: { streamId, nodeId: agent.hello?.nodeId || '' } } }))
  }, [agent.hello?.nodeId, sendFrame])

  useEffect(() => {
    if (!terminalReadySeq || !termRef.current || !fitRef.current) return
    const term = termRef.current
    const fit = fitRef.current
    term.reset()
    setStatus('connecting')
    setSessions([])
    setActiveID('')
    const streamID = globalThis.crypto?.randomUUID?.() ?? `pty-${Date.now().toString(36)}`
    streamIDRef.current = streamID
    const list = create(PtyProtocolMessageSchema, { message: { case: 'list', value: { streamId: streamID, nodeId: agent.hello?.nodeId || '' } } })
    const unsubscribe = aopClient.subscribe(PtyProtocolMessageSchema, list, (payload) => {
      if (payload.$typeName !== 'aop.pty.ProtocolMessage') return
      const frame = payload as PtyProtocolMessage
      switch (frame.message.case) {
        case 'sessions': applySessions(sessionsFromFrame(frame)); setStatus('connected'); break
        case 'opened':
        case 'attached': {
          const session = sessionFromFrame(frame)
          if (session) {
            rememberSession(session)
            activeRef.current = session.id
            setActiveID(session.id)
            markSessionRead(session.id, session)
          }
          setStatus('connected')
          try { fit.fit() } catch {}
          term.focus()
          break
        }
        case 'output': writeTerminalData(term, frame); markSessionRead(activeRef.current); break
        case 'state': {
          const session = sessionFromFrame(frame)
          if (session) rememberSession(session)
          break
        }
        case 'closed': {
          const session = sessionFromFrame(frame)
          if (session) rememberSession(session)
          activeRef.current = ''
          setActiveID('')
          sendList()
          break
        }
        case 'detached': activeRef.current = ''; setActiveID(''); setStatus('closed'); break
        case 'error': setStatus('error'); term.write(`\r\n[pty error] ${frame.message.value.message}\r\n`); break
      }
    }, { id: streamID })
    const dataDisposable = term.onData((data) => sendFrame(create(PtyProtocolMessageSchema, { message: { case: 'input', value: { streamId: streamID, data: encodeTerminalData(data) } } })))
    const resizeDisposable = term.onResize(({ cols, rows }) => sendFrame(create(PtyProtocolMessageSchema, { message: { case: 'resize', value: { streamId: streamID, cols, rows } } })))
    cleanupRef.current = () => {
      sendFrame(create(PtyProtocolMessageSchema, { message: { case: 'detach', value: { streamId: streamID } } }))
      unsubscribe()
      dataDisposable.dispose()
      resizeDisposable.dispose()
      if (streamIDRef.current === streamID) streamIDRef.current = ''
    }
    return () => { cleanupRef.current?.(); cleanupRef.current = null }
  }, [agent.hello?.nodeId, sendFrame, sendList, terminalReadySeq])

  function applySessions(next: PTYSession[]) {
    sessionsRef.current = next
    setSessions(next)
    setUnreadIDs((current) => {
      const unread = new Set(current)
      for (const session of next) {
        const seq = activitySeq(session)
        const seen = seenActivityRef.current[session.id]
        if (!activityReadyRef.current || session.id === activeRef.current) unread.delete(session.id)
        else if (seen !== undefined && seq > seen) unread.add(session.id)
        seenActivityRef.current[session.id] = seq
      }
      activityReadyRef.current = true
      return unread
    })
  }

  function markSessionRead(id: string, session?: PTYSession | null) { if (!id) return; const value = session || sessionsRef.current.find((s) => s.id === id); if (value) seenActivityRef.current[id] = activitySeq(value); setUnreadIDs((items) => { const next = new Set(items); next.delete(id); return next }) }
  function rememberSession(session: PTYSession) { sessionsRef.current = mergeSession(sessionsRef.current, session); upsertSession(setSessions, session) }
  function terminalSize() { const term = termRef.current; return term ? { cols: term.cols, rows: term.rows } : { cols: 80, rows: 24 } }
  function attachSession(session: PTYSession) { const streamId = streamIDRef.current; if (!streamId || !session.id) return; termRef.current?.reset(); activeRef.current = session.id; setActiveID(session.id); markSessionRead(session.id, session); sendFrame(create(PtyProtocolMessageSchema, { message: { case: 'attach', value: { streamId, sessionId: session.id, ...terminalSize() } } })) }
  function attachRepl() { if (replSession) attachSession(replSession); else sendList() }
  function openShell() { const streamId = streamIDRef.current; if (!streamId) return; termRef.current?.reset(); sendFrame(create(PtyProtocolMessageSchema, { message: { case: 'open', value: { streamId, nodeId: agent.hello?.nodeId || '', kind: 'shell', name: `shell-${agent.hello?.name || 'agent'}`, ...terminalSize() } } })) }
  function stopActiveSession() { const streamId = streamIDRef.current; if (!streamId || !activeID || activeSession?.kind === 'repl') return; sendFrame(create(PtyProtocolMessageSchema, { message: { case: 'kill', value: { streamId } } })) }

  const activeTitle = activeSession ? sessionTitle(activeSession) : activeID
  const summaryText = taskSummary.updates ? `${t('summaryRunning', { count: taskSummary.running })} · ${t('summaryNew', { count: taskSummary.updates })}` : t('summaryRunning', { count: taskSummary.running })
  return <div className="flex min-h-0 min-w-0 flex-1 flex-col"><TerminalHeader status={status} title={activeTitle || t('console')} actions={<><IconButton label={t('newShellPty')} onClick={openShell}><Plus className="h-3.5 w-3.5" /></IconButton><IconButton label={t('refreshSessions')} onClick={sendList}><RefreshCw className="h-3.5 w-3.5" /></IconButton><IconButton label={t('stopActiveTask')} onClick={stopActiveSession} disabled={activeSession?.kind === 'repl' || activeSession?.state !== 'running'}><Square className="h-3.5 w-3.5" /></IconButton><IconButton label={detailsOpen ? t('hideDetails') : t('showDetails')} onClick={() => setDetailsOpen((v) => !v)} active={detailsOpen}><Info className="h-3.5 w-3.5" /></IconButton></>} /><div className="flex min-h-0 min-w-0 flex-1 flex-col lg:flex-row"><SessionNavigator activeID={activeID} sessions={taskSessions} unreadIDs={unreadIDs} onSelect={attachSession} listLabel={t('tasks')} summary={summaryText} emptyText={t('noTasksYet')} header={<SessionButton active={!!replSession && replSession.id === activeID} title={t('mainRepl')} meta={replSession ? t('alwaysOn') : t('starting')} state={replSession?.state || 'running'} details={replSession ? sessionDetails(replSession) : t('mainReplStarting')} unread={!!replSession && replSession.id !== activeID && unreadIDs.has(replSession.id)} onClick={attachRepl} />} /><section className="flex min-h-0 min-w-0 flex-1 flex-col"><TerminalView onReady={handleTerminalReady} /></section>{detailsOpen && <TerminalDetails agent={agent} session={activeSession || replSession} status={status} taskSessions={taskSessions} onClose={() => setDetailsOpen(false)} />}</div></div>
}

function IconButton({ children, active, disabled, label, onClick }: { children: ReactNode; active?: boolean; disabled?: boolean; label: string; onClick: () => void }) {
  return <Tooltip><TooltipTrigger asChild><Button type="button" variant="ghost" size="icon-xs" active={active} aria-label={label} title={label} disabled={disabled} onClick={onClick} className="text-muted-foreground">{children}</Button></TooltipTrigger><TooltipContent side="bottom">{label}</TooltipContent></Tooltip>
}
