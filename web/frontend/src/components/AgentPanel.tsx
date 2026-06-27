import { useCallback, useEffect, useState } from 'react'
import { Circle, Monitor, RefreshCw, X } from 'lucide-react'
import { listAgents } from '../api'
import type { AgentInfo } from '../api'
import AgentTerminal from './terminal'
import { cn } from '@aspect/theme'
import { Spinner } from '@aspect/ui'

interface AgentPanelProps {
  open: boolean
  onClose: () => void
}

export default function AgentPanel({ open, onClose }: AgentPanelProps) {
  const { agents, error, loading, refresh, selected, selectedID, setSelectedID } = useAgentDirectory(open)
  const showAgentList = agents.length > 1

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-background/70 backdrop-blur-sm">
      <div className="flex h-full w-full max-w-7xl flex-col border-l border-border bg-card shadow-xl">
        <div className="flex h-12 shrink-0 items-center justify-between border-b border-border px-4">
          <div className="flex min-w-0 items-center gap-3">
            <Monitor className="h-4 w-4 shrink-0 text-cyber-400" />
            <div className="min-w-0">
              <div className="flex min-w-0 items-center gap-2">
                <span className="text-sm font-medium text-foreground">Agent Console</span>
                <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                  {agents.length}
                </span>
              </div>
              <div className="truncate text-xs text-muted-foreground" title={selected ? agentDetails(selected) : undefined}>
                {selected ? `${selected.name} · ${selected.busy ? 'busy' : 'idle'}` : 'No agent selected'}
              </div>
            </div>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-md p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
            aria-label="Close agents"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="min-h-0 flex-1">
          {loading ? (
            <div className="flex h-32 items-center justify-center text-muted-foreground">
              <Spinner className="h-5 w-5" />
            </div>
          ) : error ? (
            <div className="m-4 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {error}
            </div>
          ) : agents.length === 0 ? (
            <div className="flex h-32 flex-col items-center justify-center gap-2 text-muted-foreground">
              <Monitor className="h-8 w-8 opacity-20" />
              <p className="text-sm">No agents connected</p>
            </div>
          ) : (
            <div className="flex h-full min-h-0 flex-col lg:flex-row">
              {showAgentList && (
                <AgentList
                  agents={agents}
                  selectedID={selectedID}
                  onRefresh={() => refresh(true)}
                  onSelect={setSelectedID}
                />
              )}
              <section className="flex min-h-0 min-w-0 flex-1 flex-col">
                {selected && <AgentTerminal agent={selected} />}
              </section>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function useAgentDirectory(open: boolean) {
  const [agents, setAgents] = useState<AgentInfo[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [selectedID, setSelectedID] = useState('')

  const refresh = useCallback((silent = false) => {
    if (!silent) {
      setLoading(true)
      setError('')
    }
    return listAgents()
      .then((items) => {
        setAgents(items)
        setSelectedID((current) => items.some((agent) => agent.id === current) ? current : items[0]?.id || '')
      })
      .catch((err: Error) => {
        if (!silent) setError(err.message || 'Failed to load agents')
      })
      .finally(() => {
        if (!silent) setLoading(false)
      })
  }, [])

  useEffect(() => {
    if (!open) return
    refresh()
  }, [open, refresh])

  useEffect(() => {
    if (!open) return
    const interval = setInterval(() => refresh(true), 5000)
    return () => clearInterval(interval)
  }, [open, refresh])

  const selected = agents.find((agent) => agent.id === selectedID) || agents[0] || null

  return { agents, error, loading, refresh, selected, selectedID, setSelectedID }
}

function AgentList({
  agents,
  onRefresh,
  onSelect,
  selectedID,
}: {
  agents: AgentInfo[]
  onRefresh: () => void
  onSelect: (id: string) => void
  selectedID: string
}) {
  return (
    <aside className="flex max-h-52 w-full shrink-0 flex-col border-b border-border lg:max-h-none lg:w-64 lg:border-b-0 lg:border-r">
      <div className="flex h-10 items-center justify-between border-b border-border px-3">
        <span className="text-xs font-medium uppercase text-muted-foreground">Agents</span>
        <button
          type="button"
          onClick={onRefresh}
          className="rounded-md p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
          aria-label="Refresh agents"
        >
          <RefreshCw className="h-3.5 w-3.5" />
        </button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto p-2">
        {agents.map((agent) => (
          <button
            key={agent.id}
            type="button"
            onClick={() => onSelect(agent.id)}
            title={agentDetails(agent)}
            className={cn(
              'mb-1 flex w-full items-start gap-2 rounded-md px-2 py-2 text-left transition-colors',
              selectedID === agent.id
                ? 'bg-cyber-400/10 text-foreground'
                : 'text-muted-foreground hover:bg-accent hover:text-foreground',
            )}
          >
            <Circle
              className={cn(
                'mt-1 h-2.5 w-2.5 shrink-0 fill-current',
                agent.busy ? 'text-yellow-400' : 'text-cyber-400',
              )}
            />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-sm font-medium">{agent.name}</span>
              <span className="mt-0.5 block truncate text-xs">
                {agent.busy ? 'busy' : 'idle'} · {formatRelativeTime(agent.connected_at)}
              </span>
            </span>
          </button>
        ))}
      </div>
    </aside>
  )
}

function agentDetails(agent: AgentInfo) {
  const identity = agent.identity || {}
  const stats = agent.stats || {}
  const parts = [
    `name: ${agent.name}`,
    `id: ${agent.id}`,
    `state: ${agent.busy ? 'busy' : 'idle'}`,
    `connected: ${formatDateTime(agent.connected_at)}`,
    identity.hostname ? `host: ${identity.hostname}` : '',
    identity.username ? `user: ${identity.username}` : '',
    identity.working_dir ? `cwd: ${identity.working_dir}` : '',
    identity.os || identity.arch ? `runtime: ${[identity.os, identity.arch].filter(Boolean).join('/')}` : '',
    identity.pid ? `pid: ${identity.pid}` : '',
    identity.provider || identity.model ? `llm: ${[identity.provider, identity.model].filter(Boolean).join(' / ')}` : '',
    agent.commands?.length ? `commands: ${agent.commands.join(', ')}` : '',
    identity.capabilities?.length ? `capabilities: ${identity.capabilities.join(', ')}` : '',
    typeof stats.turns === 'number' ? `turns: ${stats.turns}` : '',
    typeof stats.tool_calls === 'number' ? `tool calls: ${stats.tool_calls}` : '',
    typeof stats.total_tokens === 'number' ? `tokens: ${stats.total_tokens}` : '',
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

function formatRelativeTime(iso: string): string {
  try {
    const diff = Date.now() - new Date(iso).getTime()
    const mins = Math.floor(diff / 60000)
    if (mins < 1) return 'just now'
    if (mins < 60) return `${mins}m ago`
    const hours = Math.floor(mins / 60)
    if (hours < 24) return `${hours}h ago`
    return `${Math.floor(hours / 24)}d ago`
  } catch {
    return ''
  }
}
