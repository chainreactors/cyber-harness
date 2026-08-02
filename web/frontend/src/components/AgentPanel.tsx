import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import { LoaderCircle, Monitor, Search } from 'lucide-react'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { AgentView } from '../api'
const terminalChunkReloadKey = 'aiscan-terminal-chunk-reload'
const loadAgentTerminal = () => import('./terminal')
async function loadAgentTerminalWithRecovery() {
  try {
    const module = await loadAgentTerminal()
    window.sessionStorage.removeItem(terminalChunkReloadKey)
    return module
  } catch (error) {
    if (!window.sessionStorage.getItem(terminalChunkReloadKey)) {
      window.sessionStorage.setItem(terminalChunkReloadKey, '1')
      window.location.reload()
      return new Promise<Awaited<ReturnType<typeof loadAgentTerminal>>>(() => {})
    }
    window.sessionStorage.removeItem(terminalChunkReloadKey)
    throw error
  }
}
const AgentTerminal = lazy(loadAgentTerminalWithRecovery)
import {
  Badge,
  EmptyState,
  Input,
  ListRow,
  StatusDot,
} from '@cyber/ui'
import { ConsoleDrawer } from './layout/ConsoleDrawer'

interface AgentPanelProps {
  open: boolean
  /** The roster from useChatSession — avoids a redundant listAgents poll. */
  agents: AgentView[]
  /** When opened from a deck node click, focus this agent's console. */
  focusAgentID?: string
  onClose: () => void
}

export default function AgentPanel({ open, agents: rosterAgents, focusAgentID, onClose }: AgentPanelProps) {
  const { t } = useTranslation('agent')
  const { agents, selected, selectedID, setSelectedID } = useAgentDirectory(open, rosterAgents, focusAgentID)
  const showAgentList = agents.length > 1

  return (
    <ConsoleDrawer
      open={open}
      onClose={onClose}
      icon={Monitor}
      title={t('agentConsole')}
      description={selected ? `${selected.hello?.name || ''} · ${selected.busy ? t('busy') : t('idle')}` : t('noAgentSelected')}
      titleMeta={(
        <Badge variant="secondary" size="sm" className="py-0 font-mono font-normal">
          {agents.length}
        </Badge>
      )}
    >
      {agents.length === 0 ? (
        <div className="flex h-full items-center justify-center">
          <EmptyState icon={Monitor} title={t('noAgentsConnected')} />
        </div>
      ) : (
        <div className="flex h-full min-h-0 flex-col lg:flex-row">
          {showAgentList && (
            <AgentList
              agents={agents}
              selectedID={selectedID}
              onSelect={setSelectedID}
            />
          )}
          <section className="flex min-h-0 min-w-0 flex-1 flex-col">
            {selected && (
              <Suspense fallback={(
                <div className="flex flex-1 items-center justify-center gap-2 text-xs text-muted-foreground" role="status">
                  <LoaderCircle className="h-4 w-4 animate-spin text-primary" aria-hidden="true" />
                  <span>{t('openingTerminal')}</span>
                </div>
              )}>
                <AgentTerminal agent={selected} />
              </Suspense>
            )}
          </section>
        </div>
      )}
    </ConsoleDrawer>
  )
}

// Derives selection state from the parent-provided roster (useChatSession
// already polls listAgents every 5s). No internal fetch or polling — the agent
// list is a prop, so the panel never double-polls.
function useAgentDirectory(open: boolean, roster: AgentView[], focusAgentID?: string) {
  const [selectedID, setSelectedID] = useState('')

  // Keep selection valid as the roster changes (agents disconnect / reconnect).
  useEffect(() => {
    setSelectedID((current) => roster.some((a) => a.nodeUri === current) ? current : roster[0]?.nodeUri || '')
  }, [roster])

  // Focus a specific node when the panel is opened from a deck node click.
  // Apply it exactly ONCE per open/focus request: `roster` is in the deps only
  // so we can wait for the roster to load, but the 5s poll replaces `roster`
  // every tick — without this guard the effect would re-fire and yank the
  // selection back to the focused node, defeating any manual agent switch.
  const focusAppliedRef = useRef(false)
  useEffect(() => {
    focusAppliedRef.current = false
  }, [open, focusAgentID])
  useEffect(() => {
    if (focusAppliedRef.current) return
    if (open && focusAgentID && roster.some((a) => a.nodeUri === focusAgentID)) {
      setSelectedID(focusAgentID)
      focusAppliedRef.current = true
    }
  }, [open, focusAgentID, roster])

  const selected = roster.find((agent) => agent.nodeUri === selectedID) || roster[0] || null

  return { agents: roster, selected, selectedID, setSelectedID }
}

function AgentList({
  agents,
  onSelect,
  selectedID,
}: {
  agents: AgentView[]
  onSelect: (id: string) => void
  selectedID: string
}) {
  const { t } = useTranslation('agent')
  const [query, setQuery] = useState('')

  // Busy agents first, then alphabetical — keeps active nodes at the top.
  const sorted = useMemo(
    () =>
      [...agents].sort((a, b) => {
        if (a.busy !== b.busy) return a.busy ? -1 : 1
        return (a.hello?.name || '').localeCompare(b.hello?.name || '')
      }),
    [agents],
  )
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return sorted
    return sorted.filter((a) =>
      `${a.hello?.name || ''} ${a.status?.model || ''} ${a.status?.provider || ''} ${a.hello?.runtime?.hostname || ''}`
        .toLowerCase()
        .includes(q),
    )
  }, [sorted, query])
  const busy = agents.filter((a) => a.busy).length
  const showFilter = agents.length > 6

  return (
    <aside className="flex max-h-52 w-full shrink-0 flex-col border-b border-border lg:max-h-none lg:w-64 lg:border-b-0 lg:border-r">
      <div className="flex h-10 items-center border-b border-border px-3">
        <span className="text-xs font-medium uppercase text-muted-foreground">
          {t('agents')}
          <span className="ml-1.5 font-mono text-[10px] normal-case text-muted-foreground/60">
            {busy}/{agents.length}
          </span>
        </span>
      </div>
      {showFilter && (
        <div className="border-b border-border p-2">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2 top-1/2 h-3 w-3 -translate-y-1/2 text-muted-foreground/60" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t('filterAgents')}
              aria-label={t('filterAgents')}
              className="h-8 pl-7 text-xs"
            />
          </div>
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-auto p-2">
        {filtered.length === 0 ? (
          <EmptyState compact title={t('noMatchingAgents')} />
        ) : (
          filtered.map((agent) => (
            <ListRow
              key={agent.nodeUri}
              active={selectedID === agent.nodeUri}
              leading={<StatusDot status={agent.busy ? 'warning' : 'info'} className="mt-1" />}
              onClick={() => onSelect(agent.nodeUri)}
              title={agentDetails(agent)}
              className="mb-1"
            >
              <span className="block truncate text-sm font-medium">{agent.hello?.name}</span>
              <span className="mt-0.5 block truncate text-xs">
                {agent.busy ? t('busy') : t('idle')} · {formatRelativeTime(agent.connectedAt ? timestampDate(agent.connectedAt).toISOString() : '', t)}
              </span>
            </ListRow>
          ))
        )}
      </div>
    </aside>
  )
}

function agentDetails(agent: AgentView) {
  const runtime = agent.hello?.runtime
  const status = agent.status
  const stats = agent.stats
  const parts = [
    `name: ${agent.hello?.name || ''}`,
    `id: ${agent.hello?.agentId || ''}`,
    `state: ${agent.busy ? 'busy' : 'idle'}`,
    `connected: ${agent.connectedAt ? formatDateTime(timestampDate(agent.connectedAt).toISOString()) : ''}`,
    runtime?.hostname ? `host: ${runtime.hostname}` : '',
    runtime?.username ? `user: ${runtime.username}` : '',
    runtime?.workingDir ? `cwd: ${runtime.workingDir}` : '',
    runtime?.os || runtime?.arch ? `runtime: ${[runtime.os, runtime.arch].filter(Boolean).join('/')}` : '',
    runtime?.pid ? `pid: ${runtime.pid}` : '',
    status?.provider || status?.model ? `llm: ${[status.provider, status.model].filter(Boolean).join(' / ')}` : '',
    agent.commands.length ? `commands: ${agent.commands.map((command) => command.name).join(', ')}` : '',
    agent.hello?.capabilities.length ? `capabilities: ${agent.hello.capabilities.join(', ')}` : '',
    stats ? `turns: ${stats.turns}` : '',
    stats ? `tool calls: ${stats.toolCalls}` : '',
    stats ? `tokens: ${stats.totalTokens}` : '',
  ]
  return parts.filter(Boolean).join('\n')
}

function formatDateTime(iso: string) {
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return iso
  }
}

function formatRelativeTime(iso: string, t: TFunction<'agent'>): string {
  try {
    const diff = Date.now() - new Date(iso).getTime()
    const mins = Math.floor(diff / 60000)
    if (mins < 1) return t('justNow')
    if (mins < 60) return t('minutesAgo', { count: mins })
    const hours = Math.floor(mins / 60)
    if (hours < 24) return t('hoursAgo', { count: hours })
    return t('daysAgo', { count: Math.floor(hours / 24) })
  } catch {
    return ''
  }
}
