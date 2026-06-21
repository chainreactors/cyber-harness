import { Terminal as XTerm } from '@xterm/xterm'
import type { Dispatch, SetStateAction } from 'react'
import type { TerminalMessage } from '../../api'

export interface PTYSession {
  id: string
  kind?: string
  name?: string
  command?: string
  pid?: number
  state?: string
  started_at?: string
  ended_at?: string
  exit_code?: number
  kill_cause?: string
  last_activity_at?: string
  activity_seq?: number
  output_bytes?: number
}

export type TerminalStatus = 'connecting' | 'connected' | 'closed' | 'error'

export const REPL_NAME = 'main-repl'

export function createTerminal() {
  return new XTerm({
    cursorBlink: true,
    fontFamily: 'JetBrains Mono, Consolas, ui-monospace, SFMono-Regular, monospace',
    fontSize: 13,
    lineHeight: 1.25,
    scrollback: 8000,
    convertEol: false,
    allowProposedApi: true,
    theme: {
      background: '#060a0d',
      foreground: '#d7e1e8',
      cursor: '#33d17a',
      selectionBackground: '#1f6feb55',
      black: '#0b1117',
      red: '#ff6b6b',
      green: '#33d17a',
      yellow: '#f4d35e',
      blue: '#4ea1ff',
      magenta: '#c778dd',
      cyan: '#56b6c2',
      white: '#d7e1e8',
      brightBlack: '#5c6773',
      brightRed: '#ff8a8a',
      brightGreen: '#5ee08f',
      brightYellow: '#ffe08a',
      brightBlue: '#79b8ff',
      brightMagenta: '#d8a4ec',
      brightCyan: '#7fd5df',
      brightWhite: '#ffffff',
    },
  })
}

export function parseTerminalMessage(value: unknown): TerminalMessage | null {
  if (typeof value !== 'string') return null
  try {
    return JSON.parse(value) as TerminalMessage
  } catch {
    return null
  }
}

export function writeTerminalData(term: XTerm, msg: TerminalMessage) {
  if (msg.data) {
    term.write(msg.data)
    return
  }
  if (!msg.data_b64) return
  const binary = atob(msg.data_b64)
  const data = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i += 1) {
    data[i] = binary.charCodeAt(i)
  }
  term.write(data)
}

export function sessionPayload(msg: TerminalMessage): PTYSession[] {
  const value = msg.payload?.sessions
  if (!Array.isArray(value)) return []
  return value.filter(isSession)
}

export function sessionFromPayload(msg: TerminalMessage): PTYSession | null {
  const value = msg.payload?.session
  if (isSession(value)) return value
  const id = stringPayload(msg, 'session_id')
  if (!id) return null
  const kind = stringPayload(msg, 'kind')
  const name = stringPayload(msg, 'name')
  return { id, kind, name }
}

export function stringPayload(msg: TerminalMessage, key: string): string {
  const value = msg.payload?.[key]
  return typeof value === 'string' ? value : ''
}

export function isSession(value: unknown): value is PTYSession {
  if (!value || typeof value !== 'object') return false
  return typeof (value as PTYSession).id === 'string'
}

export function upsertSession(setSessions: Dispatch<SetStateAction<PTYSession[]>>, session: PTYSession) {
  setSessions((current) => {
    const index = current.findIndex((item) => item.id === session.id)
    if (index < 0) return [...current, session]
    const next = current.slice()
    next[index] = { ...next[index], ...session }
    return next
  })
}

export function sessionTitle(session: PTYSession) {
  if (session.kind === 'repl') return 'Main REPL'
  return session.name || session.command || session.id
}

export function sessionMeta(session: PTYSession) {
  if (session.kind === 'repl') return 'console'
  return session.kind || 'task'
}

export function sessionDetails(session: PTYSession) {
  const parts = [
    `title: ${sessionTitle(session)}`,
    `id: ${session.id}`,
    session.kind ? `kind: ${session.kind}` : '',
    session.state ? `state: ${stateLabel(session.state) || session.state}` : '',
    session.command ? `command: ${session.command}` : '',
    positiveNumber(session.pid) ? `pid: ${session.pid}` : '',
    session.started_at ? `started: ${formatDateTime(session.started_at)}` : '',
    session.last_activity_at ? `activity: ${formatDateTime(session.last_activity_at)}` : '',
    session.ended_at ? `ended: ${formatDateTime(session.ended_at)}` : '',
    session.state !== 'running' && typeof session.exit_code === 'number' ? `exit: ${session.exit_code}` : '',
    session.kill_cause ? `kill: ${session.kill_cause}` : '',
    typeof session.output_bytes === 'number' ? `output: ${formatBytes(session.output_bytes)}` : '',
    typeof session.activity_seq === 'number' ? `activity seq: ${session.activity_seq}` : '',
  ]
  return parts.filter(Boolean).join('\n')
}

export function activitySeq(session: PTYSession) {
  return typeof session.activity_seq === 'number' && Number.isFinite(session.activity_seq) ? session.activity_seq : 0
}

export function compareTaskSessions(a: PTYSession, b: PTYSession) {
  const stateDelta = stateRank(a.state) - stateRank(b.state)
  if (stateDelta !== 0) return stateDelta
  return activityTime(b) - activityTime(a)
}

export function stateRank(state: string | undefined) {
  switch (state) {
    case 'running':
      return 0
    case 'failed':
    case 'killed':
      return 1
    case 'completed':
      return 2
    default:
      return 3
  }
}

export function activityTime(session: PTYSession) {
  const value = session.last_activity_at || session.ended_at || session.started_at
  if (!value) return 0
  const parsed = new Date(value).getTime()
  return Number.isFinite(parsed) ? parsed : 0
}

export function stateLabel(state: string) {
  switch (state) {
    case 'running':
      return 'running'
    case 'completed':
      return 'closed'
    case 'failed':
      return 'failed'
    case 'killed':
      return 'killed'
    default:
      return ''
  }
}

export function stateTextColor(state: string) {
  switch (state) {
    case 'running':
      return 'text-cyber-700 dark:text-cyber-300'
    case 'completed':
      return 'text-muted-foreground'
    case 'failed':
    case 'killed':
      return 'text-destructive'
    default:
      return 'text-yellow-700 dark:text-yellow-300'
  }
}

export function terminalStatusColor(status: TerminalStatus) {
  switch (status) {
    case 'connected':
      return 'bg-cyber-400/10 text-cyber-700 dark:text-cyber-300'
    case 'error':
      return 'bg-destructive/10 text-destructive'
    case 'closed':
      return 'bg-muted text-muted-foreground'
    default:
      return 'bg-yellow-400/10 text-yellow-700 dark:text-yellow-300'
  }
}

export function stateColor(state: string) {
  switch (state) {
    case 'running':
    case 'ready':
      return 'text-cyber-400'
    case 'completed':
      return 'text-muted-foreground'
    case 'killed':
    case 'failed':
      return 'text-destructive'
    default:
      return 'text-yellow-400'
  }
}

export function formatDateTime(value?: string) {
  if (!value) return undefined
  try {
    const date = new Date(value)
    if (!Number.isFinite(date.getTime()) || date.getFullYear() <= 1) return undefined
    return date.toLocaleString()
  } catch {
    return value
  }
}

export function positiveNumber(value?: number) {
  return typeof value === 'number' && value > 0 ? value : undefined
}

export function formatBytes(value?: number) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return undefined
  if (value < 1024) return `${value} B`
  const units = ['KB', 'MB', 'GB']
  let size = value / 1024
  for (const unit of units) {
    if (size < 1024) return `${size.toFixed(size >= 10 ? 0 : 1)} ${unit}`
    size /= 1024
  }
  return `${size.toFixed(1)} TB`
}
