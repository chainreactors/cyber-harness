import { useMemo, useState } from 'react'
import {
  Shield, PanelLeftClose, PanelLeft,
  MessageSquare, Plus, Trash2, Circle,
  ChevronDown, ChevronRight, Monitor, Terminal,
} from 'lucide-react'
import { Button, Tooltip, TooltipTrigger, TooltipContent } from '@aspect/ui'
import { cn } from '@aspect/theme'
import type { AgentInfo, ChatSession } from '../api'

interface Props {
  open: boolean
  onToggle: () => void
  agents?: AgentInfo[]
  sessions?: ChatSession[]
  activeSessionID: string | null
  selectedAgentID: string | null
  terminalAgentID: string | null
  onSelectAgent: (id: string) => void
  onSelectSession: (id: string) => void
  onCreateSession: (agentID: string) => void
  onDeleteSession: (id: string) => void
  onOpenTerminal: (agentID: string) => void
}

export default function SessionList({
  open, onToggle, agents = [], sessions = [],
  activeSessionID, selectedAgentID, terminalAgentID,
  onSelectAgent, onSelectSession, onCreateSession, onDeleteSession, onOpenTerminal,
}: Props) {
  const sessionsByAgent = useMemo(() => {
    const map = new Map<string, ChatSession[]>()
    for (const s of sessions) {
      const list = map.get(s.agent_id) || []
      list.push(s)
      map.set(s.agent_id, list)
    }
    return map
  }, [sessions])

  return (
    <>
      {open && (
        <button
          type="button"
          aria-label="Close sidebar overlay"
          onClick={onToggle}
          className="fixed inset-0 z-30 bg-background/60 backdrop-blur-[1px] md:hidden"
        />
      )}
      <aside
        className={cn(
          'flex flex-col border-r border-border bg-card/95 backdrop-blur-sm transition-all duration-200 ease-in-out shrink-0 md:bg-card/50',
          open
            ? 'fixed inset-y-0 left-0 z-40 w-72 shadow-xl md:relative md:inset-auto md:z-auto md:shadow-none'
            : 'w-12',
        )}
      >
        {/* Header */}
        <div className={cn('flex items-center border-b border-border', open ? 'p-3 gap-3' : 'p-2 flex-col gap-2')}>
          {open ? (
            <>
              <Shield className="w-5 h-5 text-primary shrink-0" />
              <div className="flex-1 min-w-0">
                <h1 className="text-sm font-bold text-primary">AIScan</h1>
                <div className="text-[10px] text-muted-foreground">{agents.length} agent{agents.length !== 1 ? 's' : ''}</div>
              </div>
              <Button variant="ghost" size="icon" onClick={onToggle} className="h-7 w-7 text-muted-foreground" aria-label="Collapse sidebar">
                <PanelLeftClose className="w-4 h-4" />
              </Button>
            </>
          ) : (
            <Tooltip>
              <TooltipTrigger asChild>
                <button type="button" onClick={onToggle} aria-label="Expand sidebar" className="p-1 rounded-md hover:bg-accent transition-colors">
                  <Shield className="w-5 h-5 text-primary" />
                </button>
              </TooltipTrigger>
              <TooltipContent side="right">AIScan</TooltipContent>
            </Tooltip>
          )}
        </div>

        {/* Content */}
        {open ? (
          <div className="flex-1 overflow-auto p-2 animate-fade-in">
            {agents.length === 0 ? (
              <div className="flex flex-col items-center justify-center py-8 text-center">
                <Monitor className="h-8 w-8 text-muted-foreground/20" />
                <p className="mt-2 text-xs text-muted-foreground">No agents connected</p>
                <p className="mt-1 text-[10px] text-muted-foreground/60">Start an aiscan agent to begin</p>
              </div>
            ) : (
              <div className="space-y-1">
                {agents.map((agent) => (
                  <AgentGroup
                    key={agent.id}
                    agent={agent}
                    sessions={sessionsByAgent.get(agent.id) || []}
                    isSelected={agent.id === selectedAgentID}
                    activeSessionID={activeSessionID}
                    terminalActive={agent.id === terminalAgentID}
                    onSelectAgent={() => onSelectAgent(agent.id)}
                    onSelectSession={onSelectSession}
                    onCreateSession={() => onCreateSession(agent.id)}
                    onDeleteSession={onDeleteSession}
                    onOpenTerminal={() => onOpenTerminal(agent.id)}
                  />
                ))}
              </div>
            )}
          </div>
        ) : (
          <div className="flex flex-col items-center gap-2 pt-3">
            {agents.map((agent) => (
              <Tooltip key={agent.id}>
                <TooltipTrigger asChild>
                  <button
                    type="button"
                    onClick={() => { onSelectAgent(agent.id); onToggle() }}
                    className={cn(
                      'relative p-1.5 rounded-md transition-colors',
                      agent.id === selectedAgentID ? 'bg-primary/10' : 'hover:bg-accent',
                    )}
                  >
                    <Monitor className="w-4 h-4 text-muted-foreground" />
                    <Circle className={cn(
                      'absolute -top-0.5 -right-0.5 h-2.5 w-2.5 fill-current',
                      agent.busy ? 'text-warning' : 'text-primary',
                    )} />
                  </button>
                </TooltipTrigger>
                <TooltipContent side="right">{agent.name}</TooltipContent>
              </Tooltip>
            ))}
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="icon" onClick={onToggle} className="h-7 w-7 text-muted-foreground" aria-label="Expand sidebar">
                  <PanelLeft className="w-3.5 h-3.5" />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="right">Expand sidebar</TooltipContent>
            </Tooltip>
          </div>
        )}
      </aside>
    </>
  )
}

function AgentGroup({
  agent, sessions, isSelected, activeSessionID, terminalActive,
  onSelectAgent, onSelectSession, onCreateSession, onDeleteSession, onOpenTerminal,
}: {
  agent: AgentInfo
  sessions: ChatSession[]
  isSelected: boolean
  activeSessionID: string | null
  terminalActive: boolean
  onSelectAgent: () => void
  onSelectSession: (id: string) => void
  onCreateSession: () => void
  onDeleteSession: (id: string) => void
  onOpenTerminal: () => void
}) {
  const [expanded, setExpanded] = useState(isSelected || sessions.some((s) => s.id === activeSessionID))
  const identity = agent.identity || {}
  const llm = [identity.provider, identity.model].filter(Boolean).join('/')

  function handleToggle() {
    setExpanded(!expanded)
    onSelectAgent()
  }

  return (
    <div className="rounded-lg">
      {/* Agent card */}
      <div className={cn(
        'rounded-md px-2 py-2 transition-colors',
        isSelected ? 'bg-primary/5' : 'hover:bg-accent/50',
      )}>
        <button
          type="button"
          onClick={handleToggle}
          className="flex w-full items-center gap-2 text-left"
        >
          <Circle className={cn(
            'h-2.5 w-2.5 shrink-0 fill-current',
            agent.busy ? 'text-warning' : 'text-primary',
          )} />
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-1.5">
              <span className="truncate text-xs font-semibold text-foreground">{agent.name}</span>
              <span className="text-[9px] text-muted-foreground">{agent.busy ? 'busy' : 'idle'}</span>
            </div>
            {llm && <div className="truncate text-[10px] text-muted-foreground">{llm}</div>}
          </div>
          {expanded ? (
            <ChevronDown className="h-3 w-3 shrink-0 text-muted-foreground" />
          ) : (
            <ChevronRight className="h-3 w-3 shrink-0 text-muted-foreground" />
          )}
        </button>

        {/* Action buttons on the agent card */}
        <div className="mt-1.5 flex items-center gap-1">
          <button
            type="button"
            onClick={(e) => { e.stopPropagation(); onOpenTerminal() }}
            className={cn(
              'flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-medium transition-colors',
              terminalActive
                ? 'bg-primary/15 text-primary'
                : 'text-muted-foreground hover:bg-accent hover:text-foreground',
            )}
          >
            <Terminal className="h-2.5 w-2.5" />
            Terminal
          </button>
          <button
            type="button"
            onClick={(e) => { e.stopPropagation(); onCreateSession() }}
            className="flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground hover:bg-accent hover:text-foreground transition-colors"
          >
            <Plus className="h-2.5 w-2.5" />
            New
          </button>
          {sessions.length > 0 && (
            <span className="ml-auto text-[9px] font-mono text-muted-foreground">{sessions.length} sessions</span>
          )}
        </div>
      </div>

      {/* Sessions list (second level) */}
      {expanded && sessions.length > 0 && (
        <div className="ml-3 mt-0.5 space-y-0.5 border-l border-border pl-2 animate-in fade-in slide-in-from-top-1 duration-150">
          {sessions.map((session) => (
            <SessionItem
              key={session.id}
              session={session}
              active={session.id === activeSessionID}
              onSelect={() => onSelectSession(session.id)}
              onDelete={() => onDeleteSession(session.id)}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function SessionItem({
  session, active, onSelect, onDelete,
}: {
  session: ChatSession
  active: boolean
  onSelect: () => void
  onDelete: () => void
}) {
  const title = session.title || 'New session'
  const time = new Date(session.updated_at).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })

  return (
    <div
      className={cn(
        'group flex items-center gap-1.5 rounded-md px-2 py-1 cursor-pointer transition-colors',
        active ? 'bg-primary/10 text-foreground' : 'text-muted-foreground hover:bg-accent hover:text-foreground',
      )}
    >
      <button type="button" onClick={onSelect} className="flex-1 min-w-0 text-left">
        <div className="flex items-center gap-1.5">
          <MessageSquare className="h-2.5 w-2.5 shrink-0" />
          <span className="truncate text-[11px] font-medium">{title}</span>
        </div>
        <div className="mt-0.5 text-[9px] text-muted-foreground">{time}</div>
      </button>
      <button
        type="button"
        onClick={(e) => { e.stopPropagation(); onDelete() }}
        className="invisible shrink-0 rounded p-0.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive group-hover:visible"
        aria-label="Delete session"
      >
        <Trash2 className="h-2.5 w-2.5" />
      </button>
    </div>
  )
}
