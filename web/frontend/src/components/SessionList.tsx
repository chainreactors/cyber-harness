import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  PanelLeftClose, PanelLeft,
  MessageSquare, Plus, Trash2,
  ChevronDown, ChevronRight, Monitor, Terminal,
  MonitorPlay, Loader2, AlertTriangle, Unplug,
} from 'lucide-react'
import {
  Button, Callout, Tooltip, TooltipTrigger, TooltipContent,
  Popover, PopoverTrigger, PopoverContent, EmptyState, StatusDot, ThemeToggle,
} from '@cyber/ui'
import { cn, useTheme } from '@cyber/theme'
import LanguageToggle from './LanguageToggle'
import { launchLocalAgent, listLocalAgents, stopLocalAgent } from '../api'
import type { AgentView, SessionRecord, LocalAgentView } from '../api'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import { agentActivity } from '../lib/agentActivity'
import { agentMatchesSession } from '../lib/session-agent'
import { usePolling } from '../hooks/usePolling'
import i18n from '../i18n'

function recordID(record: SessionRecord): string {
  return record.session?.id || ''
}

interface Props {
  open: boolean
  onToggle: () => void
  agents?: AgentView[]
  sessions?: SessionRecord[]
  activeSessionID: string | null
  selectedNodeURI: string | null
  terminalNodeURI: string | null
  onSelectNode: (nodeURI: string) => void
  onSelectSession: (id: string) => void
  onCreateSession: (nodeURI: string) => void
  onDeleteSession: (id: string) => void
  onOpenTerminal: (nodeURI: string) => void
}

export default function SessionList({
  open, onToggle, agents = [], sessions = [],
  activeSessionID, selectedNodeURI, terminalNodeURI,
  onSelectNode, onSelectSession, onCreateSession, onDeleteSession, onOpenTerminal,
}: Props) {
  const { t } = useTranslation('sidebar')
  const closeButtonRef = useRef<HTMLButtonElement>(null)
  const toggleRef = useRef(onToggle)
  useEffect(() => { toggleRef.current = onToggle }, [onToggle])
  useEffect(() => {
    if (!open || !window.matchMedia('(max-width: 767px)').matches) return
    const previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') toggleRef.current()
    }
    closeButtonRef.current?.focus()
    window.addEventListener('keydown', onKeyDown)
    return () => {
      window.removeEventListener('keydown', onKeyDown)
      previouslyFocused?.focus()
    }
  }, [open])
  // Attach each session to a connected agent by id-or-name (see
  // agentMatchesSession — the hub re-mints agent ids on reconnect, so match the
  // stable name too). Whatever no live agent claims is "orphaned": its bound
  // node is offline. Sessions persist server-side, so without this those
  // sessions would silently drop out of the sidebar — which nests sessions
  // under live agents — even though their transcripts are still openable.
  // Group the orphans by their bound agent name so they get a dedicated,
  // read-only "offline" section below instead of vanishing.
  const { groups, orphanGroups } = useMemo(() => {
    const claimed = new Set<string>()
    const groups = agents.map((agent) => {
      const own = sessions.filter((s) => !claimed.has(recordID(s)) && agentMatchesSession(agent, s))
      own.forEach((s) => claimed.add(recordID(s)))
      return { agent, sessions: own }
    })
    const orphanMap = new Map<string, SessionRecord[]>()
    for (const s of sessions) {
      if (claimed.has(recordID(s))) continue
      const key = s.agentName || s.session?.nodeUri || 'unknown'
      const list = orphanMap.get(key) || []
      list.push(s)
      orphanMap.set(key, list)
    }
    const orphanGroups = [...orphanMap.entries()]
      .map(([name, list]) => ({ name, sessions: list }))
      .sort((a, b) => a.name.localeCompare(b.name))
    return { groups, orphanGroups }
  }, [agents, sessions])

  // Live node count for the roster header — connected agents only (orphaned
  // sessions belong to nodes that are no longer online).
  const online = agents.length

  return (
    <>
      {open && (
        <button
          type="button"
          aria-label={t('closeSidebarOverlay')}
          onClick={onToggle}
          className="fixed inset-0 z-30 bg-background/60 backdrop-blur-[1px] md:hidden"
        />
      )}
      <aside
        className={cn(
          'surface-raised flex flex-col border-r border-border bg-card/95 backdrop-blur-sm transition-all duration-200 ease-in-out shrink-0 md:bg-card/50',
          open
            ? 'fixed inset-y-0 left-0 z-40 w-72 shadow-elevated md:relative md:inset-auto md:z-auto md:shadow-none'
            // Collapsed: a 48px icon rail on desktop, but fully hidden on phones —
            // there the drawer opens from the header's menu button, so the chat
            // gets the full width instead of a stub rail down the left edge.
            : 'w-12 max-md:hidden',
        )}
      >
        {/* Header — fleet roster identity + live node count */}
        <div className={cn('flex border-b border-border/60', open ? 'items-center gap-2 px-3 py-2.5' : 'flex-col items-center gap-2 p-2')}>
          {open ? (
            <>
              <div className="flex min-w-0 flex-1 items-center gap-2">
                {online > 0 && <StatusDot status="online" className="h-1.5 w-1.5" />}
                <span className="truncate text-xs font-medium text-muted-foreground">
                  {online > 0 ? t('onlineCount', { count: online }) : t('rosterIdle')}
                </span>
              </div>
              <LocalAgentControl />
              <Button ref={closeButtonRef} variant="ghost" size="icon" onClick={onToggle} className="h-7 w-7 text-muted-foreground" aria-label={t('collapseSidebar')}>
                <PanelLeftClose className="w-4 h-4" />
              </Button>
            </>
          ) : (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="icon-sm" onClick={onToggle} aria-label={t('expandSidebar')} className="rounded-lg text-muted-foreground">
                  <PanelLeft className="h-4 w-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="right">{t('expandSidebar')}</TooltipContent>
            </Tooltip>
          )}
        </div>

        {/* Content */}
        {open ? (
          <div className="flex-1 overflow-auto p-2.5 animate-fade-in">
            {agents.length === 0 && orphanGroups.length === 0 ? (
              <EmptyState icon={Monitor} title={t('noAgentsConnected')} description={t('startAgentToBegin')} compact />
            ) : (
              <div className="space-y-1">
                {/* No live agents, but orphaned sessions remain — keep the
                    "launch an agent" nudge as a slim banner so the guidance
                    isn't lost now that the full empty state is suppressed. */}
                {agents.length === 0 && (
                  <div className="mb-1 flex items-center gap-1.5 rounded-md border border-warning/20 bg-warning/5 px-2 py-1.5 text-[10px] text-muted-foreground">
                    <Monitor className="h-3 w-3 shrink-0 text-muted-foreground/40" />
                    <span className="min-w-0">{t('startAgentToBegin')}</span>
                  </div>
                )}
                {groups.map(({ agent, sessions: own }) => (
                  <AgentGroup
                    key={agent.nodeUri}
                    agent={agent}
                    sessions={own}
                    isSelected={agent.nodeUri === selectedNodeURI}
                    activeSessionID={activeSessionID}
                    terminalActive={agent.nodeUri === terminalNodeURI}
                    onSelectNode={() => onSelectNode(agent.nodeUri)}
                    onSelectSession={onSelectSession}
                    onCreateSession={() => onCreateSession(agent.nodeUri)}
                    onDeleteSession={onDeleteSession}
                    onOpenTerminal={() => onOpenTerminal(agent.nodeUri)}
                  />
                ))}
                {orphanGroups.length > 0 && (
                  <div className="mt-2 space-y-0.5 border-t border-border/50 pt-2">
                    <div className="flex items-center gap-1.5 px-2 pb-1">
                      <Unplug className="h-3 w-3 text-muted-foreground/50" />
                      <span className="mono-label text-muted-foreground/70">{t('offlineSessions')}</span>
                    </div>
                    {orphanGroups.map((g) => (
                      <OfflineAgentGroup
                        key={g.name}
                        name={g.name}
                        sessions={g.sessions}
                        activeSessionID={activeSessionID}
                        defaultOpen={agents.length === 0 || g.sessions.some((s) => recordID(s) === activeSessionID)}
                        onSelectSession={onSelectSession}
                        onDeleteSession={onDeleteSession}
                      />
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>
        ) : (
          <div className="flex flex-col items-center gap-2 pt-3">
            {agents.map((agent) => (
              <Tooltip key={agent.nodeUri}>
                <TooltipTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    active={agent.nodeUri === selectedNodeURI}
                    onClick={() => { onSelectNode(agent.nodeUri); onToggle() }}
                    className="relative"
                  >
                    <Monitor className="w-4 h-4 text-muted-foreground" />
                    <StatusDot
                      status={agent.busy ? 'warning' : 'online'}
                      className="absolute -top-0.5 -right-0.5 h-2.5 w-2.5"
                    />
                  </Button>
                </TooltipTrigger>
                <TooltipContent side="right">{agent.hello?.name}</TooltipContent>
              </Tooltip>
            ))}
          </div>
        )}

        <SidebarPreferences expanded={open} />
      </aside>
    </>
  )
}

function SidebarPreferences({ expanded }: { expanded: boolean }) {
  const { isDark, toggle } = useTheme()

  return (
    <div className={cn(
      'mt-auto shrink-0 border-t border-border/60',
      expanded
        ? 'flex items-center justify-end gap-1 px-3 py-2 pb-[max(0.5rem,env(safe-area-inset-bottom))]'
        : 'flex flex-col items-center gap-1 p-2',
    )}>
      <LanguageToggle />
      <div data-sidebar-theme-toggle>
        <ThemeToggle isDark={isDark} onToggle={toggle} size="sm" />
      </div>
    </div>
  )
}

function AgentGroup({
  agent, sessions, isSelected, activeSessionID, terminalActive,
  onSelectNode, onSelectSession, onCreateSession, onDeleteSession, onOpenTerminal,
}: {
  agent: AgentView
  sessions: SessionRecord[]
  isSelected: boolean
  activeSessionID: string | null
  terminalActive: boolean
  onSelectNode: () => void
  onSelectSession: (id: string) => void
  onCreateSession: () => void
  onDeleteSession: (id: string) => void
  onOpenTerminal: () => void
}) {
  const { t } = useTranslation('sidebar')
  const [expanded, setExpanded] = useState(isSelected || sessions.some((s) => recordID(s) === activeSessionID))
  const status = agent.status
  const llm = [status?.provider, status?.model].filter(Boolean).join('/')
  const act = agentActivity(agent)

  function handleToggle() {
    setExpanded(!expanded)
    onSelectNode()
  }

  return (
    <div className="rounded-lg">
      {/* Agent card */}
      <div className={cn(
        'rounded-lg px-2.5 py-2 transition-all',
        isSelected
          ? 'bg-primary/[0.06] shadow-soft ring-1 ring-inset ring-primary/20'
          : 'hover:bg-accent/50',
      )}>
        <button
          type="button"
          onClick={handleToggle}
          className="flex w-full items-center gap-2 text-left"
        >
          <StatusDot status={agent.busy ? 'warning' : 'online'} className="h-2.5 w-2.5" />
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-1.5">
              <span className="min-w-0 truncate text-xs font-semibold text-foreground">{agent.hello?.name}</span>
              <span className="shrink-0 whitespace-nowrap text-[9px] text-muted-foreground">{agent.busy ? t('busy') : t('idle')}</span>
            </div>
            {act?.kind === 'tool' ? (
              <div className="truncate text-[10px] text-warning">
                ▸ {act.tool}
                {act.detail && <span className="text-muted-foreground"> · {act.detail}</span>}
              </div>
            ) : act?.kind === 'thinking' ? (
              <div className="truncate text-[10px] text-warning">{t('working')}</div>
            ) : llm ? (
              <div className="truncate text-[10px] text-muted-foreground">{llm}</div>
            ) : null}
          </div>
          {expanded ? (
            <ChevronDown className="h-3 w-3 shrink-0 text-muted-foreground" />
          ) : (
            <ChevronRight className="h-3 w-3 shrink-0 text-muted-foreground" />
          )}
        </button>

        {/* Action buttons on the agent card */}
        <div className="mt-1.5 flex items-center gap-1">
          <Button
            variant="ghost"
            size="xs"
            active={terminalActive}
            onClick={(e) => { e.stopPropagation(); onOpenTerminal() }}
            className={terminalActive ? undefined : 'text-muted-foreground'}
          >
            <Terminal className="h-2.5 w-2.5" />
            {t('terminal')}
          </Button>
          <Button
            variant="ghost"
            size="xs"
            onClick={(e) => { e.stopPropagation(); setExpanded(true); onCreateSession() }}
            className="text-muted-foreground"
          >
            <Plus className="h-2.5 w-2.5" />
            {t('new')}
          </Button>
          {sessions.length > 0 && (
            <span className="ml-auto text-[9px] font-mono text-muted-foreground">{t('sessionsCount', { count: sessions.length })}</span>
          )}
        </div>
      </div>

      {/* Sessions list (second level) */}
      {expanded && sessions.length > 0 && (
        <div className="ml-3 mt-0.5 space-y-0.5 border-l border-border pl-2 animate-in fade-in slide-in-from-top-1 duration-150">
          {sessions.map((session) => (
            <SessionItem
              key={recordID(session)}
              session={session}
              active={recordID(session) === activeSessionID}
              onSelect={() => onSelectSession(recordID(session))}
              onDelete={() => onDeleteSession(recordID(session))}
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
  session: SessionRecord
  active: boolean
  onSelect: () => void
  onDelete: () => void
}) {
  const { t } = useTranslation('sidebar')
  const title = session.session?.title || t('newSession')
  const updatedAt = session.updatedAt ? timestampDate(session.updatedAt) : null
  const time = (updatedAt || new Date(0)).toLocaleDateString(i18n.language, { month: 'short', day: 'numeric' })

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
      <Button
        variant="ghost"
        size="icon-xs"
        onClick={(e) => { e.stopPropagation(); onDelete() }}
        // Touch devices have no hover, so a hover-only reveal would make delete
        // permanently unreachable there. Keep it visible by default; only tuck it
        // behind row-hover on pointers that actually hover (desktop). Larger hit
        // box on touch (h-8) meets the tap-target floor.
        className="h-8 w-8 shrink-0 rounded text-muted-foreground hover:bg-destructive/10 hover:text-destructive [@media(hover:hover)]:invisible [@media(hover:hover)]:h-6 [@media(hover:hover)]:w-6 [@media(hover:hover)]:group-hover:visible"
        aria-label={t('deleteSession')}
      >
        <Trash2 className="h-3 w-3" />
      </Button>
    </div>
  )
}

// OfflineAgentGroup lists sessions whose bound agent is no longer connected.
// Sessions live server-side, so when a node goes away (a local agent's process
// exiting, the hub restarting) its sessions would otherwise be stranded —
// dropped from the sidebar, which nests sessions under live agents, even though
// their transcripts are still openable. Surface them here, read-only: you can
// reopen (to read history) or delete them, but there's no connected agent to
// start a new turn on, so the terminal / new-session actions are omitted. A
// banner in the chat panel spells out that a reconnect is needed to continue.
function OfflineAgentGroup({
  name, sessions, activeSessionID, defaultOpen, onSelectSession, onDeleteSession,
}: {
  name: string
  sessions: SessionRecord[]
  activeSessionID: string | null
  defaultOpen: boolean
  onSelectSession: (id: string) => void
  onDeleteSession: (id: string) => void
}) {
  const { t } = useTranslation('sidebar')
  const [expanded, setExpanded] = useState(defaultOpen)

  return (
    <div className="rounded-lg">
      <div className="rounded-md px-2 py-1.5 transition-colors hover:bg-accent/40">
        <button
          type="button"
          onClick={() => setExpanded(!expanded)}
          className="flex w-full items-center gap-2 text-left"
        >
          <StatusDot status="idle" className="h-2.5 w-2.5 opacity-40" />
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-1.5">
              <span className="min-w-0 truncate text-xs font-medium text-muted-foreground">{name}</span>
              <span className="shrink-0 whitespace-nowrap text-[9px] text-warning">{t('agentOffline')}</span>
            </div>
          </div>
          <span className="text-[9px] font-mono text-muted-foreground/60">{t('sessionsCount', { count: sessions.length })}</span>
          {expanded ? (
            <ChevronDown className="h-3 w-3 shrink-0 text-muted-foreground" />
          ) : (
            <ChevronRight className="h-3 w-3 shrink-0 text-muted-foreground" />
          )}
        </button>
      </div>

      {expanded && sessions.length > 0 && (
        <div className="ml-3 mt-0.5 space-y-0.5 border-l border-border pl-2 animate-in fade-in slide-in-from-top-1 duration-150">
          {sessions.map((session) => (
            <SessionItem
              key={recordID(session)}
              session={session}
              active={recordID(session) === activeSessionID}
              onSelect={() => onSelectSession(recordID(session))}
              onDelete={() => onDeleteSession(recordID(session))}
            />
          ))}
        </div>
      )}
    </div>
  )
}

// LocalAgentControl is the top-left launcher for hub-hosted agents: a button that
// spawns an `aiscan agent` on the console host (wired back over loopback) and a
// popover to watch/delete the ones it started. Connected nodes also appear in the
// roster below — this popover owns their process lifecycle, not the conversation.
function LocalAgentControl() {
  const { t } = useTranslation('sidebar')
  const [open, setOpen] = useState(false)
  const [items, setItems] = useState<LocalAgentView[]>([])
  const [launching, setLaunching] = useState(false)
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    try {
      setItems(await listLocalAgents())
    } catch {
      /* transient / admin-gated — keep the last known roster */
    }
  }, [])

  // Immediate load on mount and whenever the popover opens (so a just-launched
  // node shows promptly).
  useEffect(() => {
    void refresh()
  }, [open, refresh])

  // Poll for the badge + roster; tighter cadence while the popover is open. The
  // poll pauses while the tab is hidden and catches up on return to foreground.
  usePolling(refresh, open ? 2500 : 8000)

  const launch = async () => {
    setLaunching(true)
    setError('')
    try {
      await launchLocalAgent()
      await refresh()
    } catch (err) {
      setError((err as Error)?.message || t('launchLocal'))
    } finally {
      setLaunching(false)
    }
  }

  const remove = async (name: string) => {
    setError('')
    try {
      await stopLocalAgent(name)
      await refresh()
    } catch (err) {
      setError((err as Error)?.message || t('stopLocal'))
    }
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <Tooltip>
        <TooltipTrigger asChild>
          <PopoverTrigger asChild>
            <Button variant="ghost" size="icon-xs" active={open} aria-label={t('launchLocal')} className="relative">
              <MonitorPlay className="h-4 w-4" />
              {items.length > 0 && (
                <span className="absolute -right-0.5 -top-0.5 flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-primary px-0.5 text-[8px] font-bold text-primary-foreground">
                  {items.length > 9 ? '9+' : items.length}
                </span>
              )}
            </Button>
          </PopoverTrigger>
        </TooltipTrigger>
        <TooltipContent side="bottom">{t('localAgents')}</TooltipContent>
      </Tooltip>

      <PopoverContent align="end" sideOffset={6} className="z-[70] w-64 rounded-xl p-2 shadow-elevated">
        <div className="px-1 pb-1.5">
          <span className="text-xs font-semibold text-foreground">{t('localAgents')}</span>
        </div>

        {error && (
          <Callout tone="destructive" icon={<AlertTriangle className="h-3 w-3" />} className="mb-1.5 gap-1.5 px-2 py-1.5 text-[11px]">
            {error}
          </Callout>
        )}

        <div className="max-h-56 space-y-0.5 overflow-auto">
          {items.length === 0 ? (
            <p className="px-1 py-3 text-center text-[11px] text-muted-foreground/70">{t('noLocalAgents')}</p>
          ) : (
            items.map((it) => <LocalAgentRow key={it.name} item={it} onRemove={() => void remove(it.name)} />)
          )}
        </div>

        <Button variant="soft" size="sm" onClick={() => void launch()} disabled={launching} className="mt-1.5 h-8 w-full gap-1.5 text-xs">
          {launching ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 w-3.5" />}
          {launching ? t('launching') : t('launchLocal')}
        </Button>
        <p className="mt-1 px-1 text-[10px] leading-snug text-muted-foreground/70">{t('localHint')}</p>
      </PopoverContent>
    </Popover>
  )
}

function LocalAgentRow({ item, onRemove }: { item: LocalAgentView; onRemove: () => void }) {
  const { t } = useTranslation('sidebar')
  const connected = item.registered

  return (
    <div className="group flex items-center gap-2 rounded-md px-1.5 py-1.5 hover:bg-accent/50">
      <StatusDot status={connected ? 'online' : 'pending'} className="h-2 w-2" />
      <div className="min-w-0 flex-1">
        <div className="truncate text-xs font-medium text-foreground">{item.name}</div>
        <div className="truncate text-[10px] text-muted-foreground">
          {connected ? t('localConnected') : t('localStarting')}
          {item.busy ? ` · ${t('busy')}` : ''}
          {item.pid ? ` · pid ${item.pid}` : ''}
        </div>
      </div>
      <Button
        variant="ghost"
        size="icon-xs"
        onClick={onRemove}
        aria-label={t('stopLocal')}
        title={t('stopLocal')}
        className="h-6 w-6 shrink-0 rounded text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
      >
        <Trash2 className="h-3 w-3" />
      </Button>
    </div>
  )
}
