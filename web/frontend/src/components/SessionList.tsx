import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  PanelLeftClose, PanelLeft, ChevronDown, ChevronRight, Monitor,
  List, MessageSquare, MoreHorizontal, Plus, Trash2,
  Archive, ArchiveRestore, Pencil, Check, X,
} from 'lucide-react'
import {
  Button, Tooltip, TooltipTrigger, TooltipContent,
  ThemeToggle, DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator,
} from '@cyber/ui'
import { cn, useTheme } from '@cyber/theme'
import LanguageToggle from './LanguageToggle'
import type { AgentView, SessionRecord, SessionFilters } from '../api'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import i18n from '../i18n'

function recordID(record: SessionRecord): string {
  return record.session?.id || ''
}

interface Props {
  open: boolean
  onToggle: () => void
  agents?: AgentView[]
  sessions?: SessionRecord[]
  filters: SessionFilters
  onFilter: (patch: Partial<SessionFilters>) => void
  onUpdateSession: (id: string, patch: { title?: string; archived?: boolean }) => Promise<void>
  activeSessionID: string | null
  activeSessionNodeID: string | null
  activeSessionBusy: boolean
  selectedNodeID: string | null
  onSelectSession: (id: string) => void
  onCreateSession: (nodeID: string) => void
  onDeleteSession: (id: string) => void
}

export default function SessionList({
  open, onToggle, agents = [], sessions = [], filters, onFilter,
  activeSessionID, activeSessionNodeID, activeSessionBusy, selectedNodeID,
  onSelectSession, onCreateSession, onDeleteSession, onUpdateSession,
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
  const revealedSessionRef = useRef<string | null>(null)
  useEffect(() => {
    if (!activeSessionID || !activeSessionNodeID || revealedSessionRef.current === activeSessionID) return
    const visible = sessions.some((session) => recordID(session) === activeSessionID)
    if (!visible) return
    revealedSessionRef.current = activeSessionID
    if (filters.nodeId !== activeSessionNodeID) onFilter({ nodeId: activeSessionNodeID })
  }, [activeSessionID, activeSessionNodeID, sessions, filters.nodeId, onFilter])
  const newTaskNodeID = filters.nodeId
    ? agents.find((agent) => agent.hello?.nodeId === filters.nodeId)?.hello?.nodeId
    : agents.find((agent) => agent.hello?.nodeId === selectedNodeID)?.hello?.nodeId || agents[0]?.hello?.nodeId
  const groups = new Map<string, { name: string; online: boolean; sessions: SessionRecord[] }>()
  for (const agent of agents) {
    const id = agent.hello?.nodeId
    if (id) groups.set(id, { name: agent.hello?.name || id, online: true, sessions: [] })
  }
  for (const session of sessions) {
    const id = session.session?.nodeId
    if (!id) continue
    const group = groups.get(id) || { name: session.agentName || id, online: false, sessions: [] }
    group.sessions.push(session)
    groups.set(id, group)
  }
  if (filters.nodeId && !groups.has(filters.nodeId)) groups.set(filters.nodeId, { name: filters.nodeId, online: false, sessions: [] })
  const visibleGroups = [...groups].filter(([id, group]) => (!filters.search && !filters.archived) || group.sessions.length > 0 || id === filters.nodeId)
  const visibleTaskCount = filters.nodeId ? groups.get(filters.nodeId)?.sessions.length || 0 : sessions.length
  const emptyMessage = filters.search ? 'noMatchingTasks' : filters.archived ? 'noArchivedTasks' : filters.nodeId ? 'noNodeTasks' : 'noTasksYet'

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
            ? 'fixed bottom-0 left-0 top-[calc(env(safe-area-inset-top)+3rem)] z-40 w-72 shadow-elevated md:relative md:inset-auto md:z-auto md:shadow-none'
            // Collapsed: a 48px icon rail on desktop, but fully hidden on phones —
            // there the drawer opens from the header's menu button, so the chat
            // gets the full width instead of a stub rail down the left edge.
            : 'w-12 max-md:hidden',
        )}
      >
        <div className={cn('flex border-b border-border/60', open ? 'items-center gap-2 px-3 py-2.5' : 'flex-col items-center gap-2 p-2')}>
          {open ? (
            <>
              <span className="min-w-0 flex-1 truncate text-xs font-semibold text-foreground">{t('tasks')}</span>
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

        {open && <div className="flex items-center gap-1 border-b border-border/60 p-2.5">
          <input aria-label={t('searchTasks')} placeholder={t('searchTasks')} value={filters.search} onChange={(event) => onFilter({ search: event.target.value })} className="w-full rounded-md border border-border bg-background px-2 py-1.5 text-xs" />
          <Button size="icon-xs" variant={filters.archived ? 'secondary' : 'ghost'} aria-label={t('archived')} title={t('archived')} aria-pressed={filters.archived} onClick={() => onFilter({ archived: !filters.archived })}><Archive className="h-3.5 w-3.5" /></Button>
        </div>}

        {open ? (
          <div className="flex-1 overflow-auto p-2.5 animate-fade-in">
            <div className="space-y-1">
              <button type="button" aria-pressed={!filters.nodeId} onClick={() => onFilter({ nodeId: '' })} className={cn('flex h-8 w-full items-center gap-2 rounded-md px-2 text-left text-xs', !filters.nodeId ? 'bg-accent font-medium text-foreground' : 'text-muted-foreground hover:bg-accent/50 hover:text-foreground')}>
                <List className="h-3.5 w-3.5 shrink-0" />
                <span className="min-w-0 flex-1 truncate">{t('allTasks')}</span>
                <span className="tabular-nums text-[10px] text-muted-foreground">{sessions.length}</span>
              </button>
              {visibleGroups.map(([id, group]) => {
                const expanded = !filters.nodeId || filters.nodeId === id
                return <div key={id} data-node-id={id}>
                  <div className={cn('group flex h-9 items-center gap-1 rounded-md px-1.5', filters.nodeId === id ? 'bg-accent' : 'hover:bg-accent/50')}>
                    <button type="button" aria-expanded={expanded} aria-pressed={filters.nodeId === id} onClick={() => onFilter({ nodeId: id })} className="flex min-w-0 flex-1 items-center gap-2 text-left">
                      {expanded ? <ChevronDown className="h-3.5 w-3.5 shrink-0" /> : <ChevronRight className="h-3.5 w-3.5 shrink-0" />}
                      <Monitor className={cn('h-3.5 w-3.5 shrink-0', group.online ? 'text-primary' : 'text-muted-foreground/50')} />
                      <span className="truncate text-xs font-medium">{group.name}</span>
                      {!group.online && <span className="shrink-0 text-[9px] text-warning">{t('agentOffline')}</span>}
                      <span className="ml-auto shrink-0 text-[10px] tabular-nums text-muted-foreground">{group.sessions.length}</span>
                    </button>
                    {group.online && <Button size="icon-xs" variant="ghost" aria-label={t('newTaskForNode', { name: group.name })} title={t('newTask')} onClick={() => { onFilter({ nodeId: id, search: '', archived: false }); onCreateSession(id) }}><Plus className="h-3.5 w-3.5" /></Button>}
                  </div>
                  {expanded && group.sessions.length > 0 && <div className="ml-4 space-y-0.5 border-l border-border/70 pl-1.5">
                    {group.sessions.map((session) => <SessionItem
                      key={recordID(session)}
                      session={session}
                      active={recordID(session) === activeSessionID}
                      busy={recordID(session) === activeSessionID && activeSessionBusy}
                      onSelect={() => onSelectSession(recordID(session))}
                      onDelete={() => onDeleteSession(recordID(session))}
                      onUpdate={(patch) => onUpdateSession(recordID(session), patch)}
                    />)}
                  </div>}
                </div>
              })}
              {visibleTaskCount === 0 && <div className="space-y-1 px-2 py-3 text-xs text-muted-foreground">
                <p>{t(emptyMessage)}</p>
                {filters.search ? <button type="button" className="text-primary hover:underline" onClick={() => onFilter({ search: '' })}>{t('clearSearch')}</button>
                  : filters.archived ? <button type="button" className="text-primary hover:underline" onClick={() => onFilter({ archived: false })}>{t('showActiveTasks')}</button>
                    : filters.nodeId ? <button type="button" className="text-primary hover:underline" onClick={() => onFilter({ nodeId: '' })}>{t('allTasks')}</button>
                      : null}
              </div>}
            </div>
          </div>
        ) : (
          <div className="flex flex-col items-center pt-3">
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="icon-sm" disabled={!newTaskNodeID} onClick={() => newTaskNodeID && onCreateSession(newTaskNodeID)} aria-label={t('newTask')}>
                  <Plus className="h-4 w-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="right">{t('newTask')}</TooltipContent>
            </Tooltip>
          </div>
        )}

        <SidebarPreferences expanded={open} />
      </aside>
    </>
  )
}

function SidebarPreferences({ expanded }: { expanded: boolean }) {
  const { isDark, toggle } = useTheme()
  const { t } = useTranslation('sidebar')

  return (
    <div className={cn(
      'mt-auto shrink-0 border-t border-border/60',
      expanded
        ? 'flex items-center justify-end gap-1 px-3 py-2 pb-[max(0.5rem,env(safe-area-inset-bottom))]'
        : 'flex flex-col items-center gap-1 p-2',
    )}>
      <LanguageToggle />
      <div data-sidebar-theme-toggle>
        <ThemeToggle
          isDark={isDark}
          onToggle={toggle}
          size="sm"
          toLightLabel={t('switchToLight')}
          toDarkLabel={t('switchToDark')}
        />
      </div>
    </div>
  )
}

function SessionItem({
  session, active, busy, onSelect, onDelete, onUpdate,
}: {
  session: SessionRecord
  active: boolean
  busy: boolean
  onSelect: () => void
  onDelete: () => void
  onUpdate: (patch: { title?: string; archived?: boolean }) => Promise<void>
}) {
  const { t } = useTranslation('sidebar')
  const [editing, setEditing] = useState(false)
  const [titleDraft, setTitleDraft] = useState('')
  const [saving, setSaving] = useState(false)
  const itemRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (active) itemRef.current?.scrollIntoView({ block: 'nearest' })
  }, [active])
  const save = async (patch: { title?: string; archived?: boolean }) => {
    if (saving) return
    setSaving(true)
    try { await onUpdate(patch); setEditing(false) } catch { /* parent displays error */ }
    finally { setSaving(false) }
  }

  const title = session.session?.title || t('newSession')
  const updatedAt = session.updatedAt ? timestampDate(session.updatedAt) : null
  const time = (updatedAt || new Date(0)).toLocaleDateString(i18n.language, { month: 'short', day: 'numeric' })
  const closed = session.session?.state === 'closed'

  return (
    <div
      ref={itemRef}
      data-session-id={recordID(session)}
      className={cn(
        'group flex min-h-10 items-center gap-1 rounded-md px-2 py-1 transition-colors',
        active ? 'bg-primary/10 text-foreground' : 'text-muted-foreground hover:bg-accent hover:text-foreground',
      )}
    >
      {editing ? <form className="flex min-w-0 flex-1 items-center" onSubmit={(event) => { event.preventDefault(); void save({ title: titleDraft }) }}>
        <input autoFocus aria-label={t('renameTask')} maxLength={200} value={titleDraft} onChange={(event) => setTitleDraft(event.target.value)} className="min-w-0 flex-1 rounded border border-border bg-background px-1 text-xs" />
        <button disabled={saving || !titleDraft.trim()} aria-label={t('saveTitle')}><Check className="h-3 w-3" /></button>
        <button type="button" onClick={() => setEditing(false)} aria-label={t('cancelEdit')}><X className="h-3 w-3" /></button>
      </form> : <>
      <button type="button" onClick={onSelect} className="flex-1 min-w-0 text-left">
        <div className="flex items-center gap-1.5">
          {busy ? <span className="h-2 w-2 shrink-0 animate-pulse rounded-full bg-warning" role="status" aria-label={t('runningTask')} title={t('runningTask')} />
            : closed ? <span className="h-2 w-2 shrink-0 rounded-full bg-muted-foreground/50" role="status" aria-label={t('closedTask')} title={t('closedTask')} />
              : <MessageSquare className="h-2.5 w-2.5 shrink-0" />}
          <span className="truncate text-[11px] font-medium">{title}</span>
        </div>
        <div className="mt-0.5 text-[9px] text-muted-foreground">{time}</div>
      </button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button size="icon-xs" variant="ghost" aria-label={t('taskActions', { title })} className="h-7 w-7 shrink-0 text-muted-foreground"><MoreHorizontal className="h-3.5 w-3.5" /></Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onSelect={() => { setTitleDraft(title); setEditing(true) }}><Pencil className="mr-2 h-3.5 w-3.5" />{t('renameTask')}</DropdownMenuItem>
          <DropdownMenuItem disabled={saving} onSelect={() => void save({ archived: !session.archived })}>{session.archived ? <ArchiveRestore className="mr-2 h-3.5 w-3.5" /> : <Archive className="mr-2 h-3.5 w-3.5" />}{t(session.archived ? 'restoreTask' : 'archiveTask')}</DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem className="text-destructive focus:text-destructive" onSelect={onDelete}><Trash2 className="mr-2 h-3.5 w-3.5" />{t('deleteSession')}</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      </>}
    </div>
  )
}
