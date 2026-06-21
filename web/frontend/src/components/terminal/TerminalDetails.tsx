import type { ReactNode } from 'react'
import { X } from 'lucide-react'
import type { AgentInfo } from '../../api'
import { cn } from '../../lib/utils'
import { Tooltip } from '../ui/tooltip'
import type { PTYSession, TerminalStatus } from './terminal-utils'
import { formatBytes, formatDateTime, positiveNumber, sessionTitle, stateLabel } from './terminal-utils'

export function TerminalDetails({
  agent,
  onClose,
  session,
  status,
  taskSessions,
}: {
  agent: AgentInfo
  onClose: () => void
  session: PTYSession | null
  status: TerminalStatus
  taskSessions: PTYSession[]
}) {
  const identity = agent.identity || {}
  const stats = agent.stats || {}
  const running = taskSessions.filter((item) => item.state === 'running').length
  const closed = taskSessions.length - running

  return (
    <aside className="flex max-h-72 w-full shrink-0 flex-col border-t border-border bg-card lg:max-h-none lg:w-80 lg:border-l lg:border-t-0">
      <div className="flex h-10 shrink-0 items-center justify-between border-b border-border px-3">
        <span className="text-xs font-medium uppercase text-muted-foreground">Details</span>
        <IconButton label="Close details" onClick={onClose}>
          <X className="h-3.5 w-3.5" />
        </IconButton>
      </div>
      <div className="min-h-0 flex-1 overflow-auto p-3 text-xs">
        <DetailGroup title="Agent">
          <DetailRow label="Name" value={agent.name} />
          <DetailRow label="ID" value={agent.id} mono />
          <DetailRow label="State" value={agent.busy ? 'busy' : 'idle'} />
          <DetailRow label="Connected" value={formatDateTime(agent.connected_at)} />
          <DetailRow label="Host" value={identity.hostname} />
          <DetailRow label="User" value={identity.username} />
          <DetailRow label="Runtime" value={[identity.os, identity.arch].filter(Boolean).join('/')} />
          <DetailRow label="PID" value={identity.pid} />
          <DetailRow label="CWD" value={identity.working_dir} mono />
          <DetailRow label="LLM" value={[identity.provider, identity.model].filter(Boolean).join(' / ') || 'offline'} />
          <DetailRow label="Space" value={identity.space} />
        </DetailGroup>

        <DetailGroup title="Active Session">
          <DetailRow label="Console" value={status} />
          {session ? (
            <>
              <DetailRow label="Title" value={sessionTitle(session)} />
              <DetailRow label="ID" value={session.id} mono />
              <DetailRow label="Kind" value={session.kind} />
              <DetailRow label="State" value={stateLabel(session.state || '') || session.state} />
              <DetailRow label="Command" value={session.command} mono />
              <DetailRow label="PID" value={positiveNumber(session.pid)} />
              <DetailRow label="Started" value={formatDateTime(session.started_at)} />
              <DetailRow label="Activity" value={formatDateTime(session.last_activity_at)} />
              <DetailRow label="Ended" value={formatDateTime(session.ended_at)} />
              <DetailRow label="Exit" value={session.state === 'running' ? undefined : session.exit_code} />
              <DetailRow label="Kill" value={session.kill_cause} />
              <DetailRow label="Output" value={formatBytes(session.output_bytes)} />
            </>
          ) : (
            <DetailRow label="State" value="starting" />
          )}
        </DetailGroup>

        <DetailGroup title="Tasks">
          <DetailRow label="Total" value={taskSessions.length} />
          <DetailRow label="Running" value={running} />
          <DetailRow label="Closed" value={closed} />
          <DetailRow label="Commands" value={agent.commands?.join(', ')} />
          <DetailRow label="Capabilities" value={identity.capabilities?.join(', ')} />
        </DetailGroup>

        <DetailGroup title="Stats">
          <DetailRow label="Turns" value={stats.turns} />
          <DetailRow label="Tools" value={stats.tool_calls} />
          <DetailRow label="Running" value={stats.running_tools} />
          <DetailRow label="Tokens" value={stats.total_tokens} />
          <DetailRow label="Assets" value={stats.assets} />
          <DetailRow label="Loots" value={stats.loots} />
          <DetailRow label="Last" value={stats.last_event} />
        </DetailGroup>
      </div>
    </aside>
  )
}

function IconButton({
  children,
  label,
  onClick,
}: {
  children: ReactNode
  label: string
  onClick: () => void
}) {
  return (
    <Tooltip content={label} side="bottom">
      <button
        type="button"
        aria-label={label}
        title={label}
        onClick={onClick}
        className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground"
      >
        {children}
      </button>
    </Tooltip>
  )
}

export function DetailGroup({ children, title }: { children: ReactNode; title: string }) {
  return (
    <section className="mb-4 last:mb-0">
      <div className="mb-2 text-[10px] font-medium uppercase text-muted-foreground">{title}</div>
      <div className="space-y-1.5">{children}</div>
    </section>
  )
}

export function DetailRow({ label, mono, value }: { label: string; mono?: boolean; value?: ReactNode }) {
  if (value === undefined || value === null || value === '') return null
  return (
    <div className="grid grid-cols-[76px_minmax(0,1fr)] gap-2">
      <div className="text-muted-foreground">{label}</div>
      <div className={cn('min-w-0 break-words text-foreground', mono && 'font-mono')}>{value}</div>
    </div>
  )
}
