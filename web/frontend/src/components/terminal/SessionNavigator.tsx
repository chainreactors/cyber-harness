import { Circle } from 'lucide-react'
import { cn } from '../../lib/utils'
import type { PTYSession } from './terminal-utils'
import { sessionDetails, sessionMeta, sessionTitle, stateColor, stateLabel, stateTextColor } from './terminal-utils'

export function SessionNavigator({
  activeID,
  replSession,
  summary,
  taskSessions,
  unreadIDs,
  onAttachRepl,
  onAttach,
}: {
  activeID: string
  replSession: PTYSession | null
  summary: { running: number; updates: number }
  taskSessions: PTYSession[]
  unreadIDs: Set<string>
  onAttachRepl: () => void
  onAttach: (session: PTYSession) => void
}) {
  return (
    <aside className="flex max-h-64 w-full shrink-0 flex-col border-b border-border lg:max-h-none lg:w-64 lg:border-b-0 lg:border-r">
      <div className="border-b border-border p-2">
        <SessionButton
          active={!!replSession && replSession.id === activeID}
          title="Main REPL"
          meta={replSession ? 'always on' : 'starting'}
          state={replSession?.state || 'running'}
          details={replSession ? sessionDetails(replSession) : 'Main REPL is starting'}
          unread={replSession ? replSession.id !== activeID && unreadIDs.has(replSession.id) : false}
          onClick={onAttachRepl}
        />
      </div>
      <div className="flex h-9 shrink-0 items-center justify-between gap-2 border-b border-border px-3 text-[10px] uppercase text-muted-foreground">
        <span>Tasks</span>
        <span className="truncate">
          {summary.running} running{summary.updates ? ` · ${summary.updates} new` : ''}
        </span>
      </div>
      <div className="min-h-0 flex-1 overflow-auto p-2">
        {taskSessions.length === 0 ? (
          <div className="px-2 py-3 text-xs text-muted-foreground">No tasks yet</div>
        ) : (
          taskSessions.map((session) => (
            <SessionButton
              key={session.id}
              active={session.id === activeID}
              title={sessionTitle(session)}
              meta={sessionMeta(session)}
              command={session.command}
              state={session.state || 'unknown'}
              details={sessionDetails(session)}
              unread={session.id !== activeID && unreadIDs.has(session.id)}
              onClick={() => onAttach(session)}
            />
          ))
        )}
      </div>
    </aside>
  )
}

function SessionButton({
  active,
  command,
  details,
  meta,
  onClick,
  state,
  title,
  unread,
}: {
  active: boolean
  command?: string
  details?: string
  meta: string
  onClick: () => void
  state: string
  title: string
  unread?: boolean
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={details}
      className={cn(
        'mb-1 flex w-full items-start gap-2 rounded-md px-2 py-2 text-left text-xs transition-colors',
        active
          ? 'bg-cyber-400/10 text-foreground'
          : unread
            ? 'bg-cyber-400/5 text-foreground hover:bg-cyber-400/10'
            : 'text-muted-foreground hover:bg-accent hover:text-foreground',
      )}
    >
      <span className="relative mt-1 shrink-0">
        <Circle className={cn('h-2.5 w-2.5 fill-current', stateColor(state))} />
        {unread && <span className="absolute -right-0.5 -top-0.5 h-1.5 w-1.5 rounded-full bg-cyber-400" />}
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="min-w-0 flex-1 truncate font-medium">{title}</span>
          <span className={cn('shrink-0 text-[10px]', stateTextColor(state))}>{stateLabel(state)}</span>
        </span>
        <span className="mt-0.5 block truncate font-mono">{meta}</span>
        {command && <span className="mt-0.5 block truncate font-mono opacity-70">{command}</span>}
      </span>
    </button>
  )
}
