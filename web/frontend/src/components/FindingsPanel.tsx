import { useMemo, useState } from 'react'
import { AlertCircle, CheckCircle2, Crosshair, Key, Radar, Shield } from 'lucide-react'
import type { ScanResult } from '../api'
import { buildFindings, PRIORITY_ORDER, type FindingItem, type FindingPriority } from '../lib/scan-result'
import { cn } from '@aspect/theme'
import MarkdownContent from './MarkdownContent'

interface FindingsPanelProps {
  result: ScanResult
}

type FilterValue = 'all' | FindingPriority | 'ai_verified'

const PRIORITY_STYLE = {
  critical: { bg: 'bg-red-500/15', text: 'text-red-600 dark:text-red-400', border: 'border-red-500/30', dot: 'bg-red-500' },
  high: { bg: 'bg-orange-500/15', text: 'text-orange-600 dark:text-orange-400', border: 'border-orange-500/30', dot: 'bg-orange-500' },
  medium: { bg: 'bg-yellow-500/15', text: 'text-yellow-600 dark:text-yellow-400', border: 'border-yellow-500/30', dot: 'bg-yellow-500' },
  low: { bg: 'bg-green-500/15', text: 'text-green-600 dark:text-green-400', border: 'border-green-500/30', dot: 'bg-green-500' },
  info: { bg: 'bg-blue-500/15', text: 'text-blue-600 dark:text-blue-400', border: 'border-blue-500/30', dot: 'bg-blue-500' },
} as const

export default function FindingsPanel({ result }: FindingsPanelProps) {
  const findings = useMemo(() => buildFindings(result), [result])
  const [filter, setFilter] = useState<FilterValue>('all')

  const filtered = useMemo(() => {
    if (filter === 'all') return findings
    if (filter === 'ai_verified') return findings.filter(f => f.source === 'verify' && f.status === 'confirmed')
    return findings.filter(f => f.priority === filter)
  }, [findings, filter])

  const grouped = useMemo(() => {
    const groups: Record<string, FindingItem[]> = {}
    for (const f of filtered) {
      ;(groups[f.priority] ||= []).push(f)
    }
    return groups
  }, [filtered])

  if (findings.length === 0) {
    return (
      <div className="py-12 text-center text-sm text-muted-foreground">
        <Shield className="mx-auto mb-3 h-8 w-8 opacity-40" />
        <p>No findings yet.</p>
      </div>
    )
  }

  const aiCount = findings.filter(f => f.source === 'verify' && f.status === 'confirmed').length

  return (
    <div className="space-y-4 animate-fade-in">
      <div className="flex flex-wrap items-center gap-1.5">
        <FilterChip active={filter === 'all'} onClick={() => setFilter('all')}>
          All ({findings.length})
        </FilterChip>
        {PRIORITY_ORDER.map(p => {
          const count = findings.filter(f => f.priority === p).length
          if (count === 0) return null
          return (
            <FilterChip key={p} active={filter === p} onClick={() => setFilter(p)}>
              <span className={cn('inline-block h-2 w-2 rounded-full', PRIORITY_STYLE[p].dot)} />
              {p.charAt(0).toUpperCase() + p.slice(1)} ({count})
            </FilterChip>
          )
        })}
        {aiCount > 0 && (
          <FilterChip active={filter === 'ai_verified'} onClick={() => setFilter('ai_verified')}>
            <CheckCircle2 className="h-3 w-3 text-green-600 dark:text-green-400" />
            AI Verified ({aiCount})
          </FilterChip>
        )}
      </div>

      {PRIORITY_ORDER.map(priority => {
        const items = grouped[priority]
        if (!items || items.length === 0) return null
        const style = PRIORITY_STYLE[priority]
        return (
          <div key={priority} className={cn('rounded-lg border', style.border)}>
            <div className={cn('flex items-center gap-2 border-b px-4 py-2 text-xs font-semibold uppercase', style.border, style.bg, style.text)}>
              <span className={cn('h-2.5 w-2.5 rounded-full', style.dot)} />
              {priority} ({items.length})
            </div>
            <div className="divide-y divide-border/50">
              {items.map(item => (
                <FindingCard key={item.id} item={item} />
              ))}
            </div>
          </div>
        )
      })}
    </div>
  )
}

function FindingCard({ item }: { item: FindingItem }) {
  const [expanded, setExpanded] = useState(false)

  return (
    <div className="p-3 text-xs">
      <div className="flex items-start gap-2">
        <FindingKindIcon kind={item.kind} />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium text-foreground break-words">{item.title}</span>
            <span className="rounded bg-secondary px-1.5 py-0.5 text-[10px] text-muted-foreground">{item.kind}</span>
            <FindingSourceBadge source={item.source} status={item.status} />
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
            <span className="break-all font-mono">{item.target}</span>
            {item.tags.slice(0, 5).map(tag => (
              <span key={tag} className="rounded bg-secondary/80 px-1.5 py-0.5 text-[10px]">{tag}</span>
            ))}
            {item.tags.length > 5 && (
              <span className="text-[10px]">+{item.tags.length - 5}</span>
            )}
          </div>
        </div>
      </div>

      {item.detail && (
        <div className="mt-2">
          {!expanded ? (
            <button
              type="button"
              className="text-[11px] text-cyber-700 dark:text-cyber-400 hover:underline"
              onClick={() => setExpanded(true)}
            >
              Show AI Analysis
            </button>
          ) : (
            <div className="mt-1 rounded-md border-l-4 border-l-cyber-400 bg-cyber-500/5 p-3">
              <div className="mb-1.5 flex items-center justify-between">
                <span className="text-[10px] font-medium uppercase text-cyber-700 dark:text-cyber-400">
                  {item.source === 'verify' ? 'AI Verification' : item.source === 'sniper' ? 'CVE Intelligence' : 'Analysis'}
                </span>
                <button
                  type="button"
                  className="text-[10px] text-muted-foreground hover:text-foreground"
                  onClick={() => setExpanded(false)}
                >
                  Hide
                </button>
              </div>
              <div className="max-h-72 overflow-auto text-muted-foreground">
                <MarkdownContent content={item.detail} compact muted />
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function FindingKindIcon({ kind }: { kind: FindingItem['kind'] }) {
  switch (kind) {
    case 'vuln': return <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-red-600 dark:text-red-400" />
    case 'weakpass': return <Key className="mt-0.5 h-3.5 w-3.5 shrink-0 text-orange-600 dark:text-orange-400" />
    case 'fingerprint': return <Shield className="mt-0.5 h-3.5 w-3.5 shrink-0 text-yellow-600 dark:text-yellow-400" />
    default: return <Shield className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
  }
}

function FindingSourceBadge({ source, status }: { source?: string; status?: string }) {
  if (source === 'verify' && status === 'confirmed') {
    return (
      <span className="inline-flex items-center gap-1 rounded bg-green-400/10 px-1.5 py-0.5 text-[10px] font-medium text-green-700 dark:text-green-400">
        <CheckCircle2 className="h-3 w-3" />AI Verified
      </span>
    )
  }
  if (source === 'verify' && status === 'not_confirmed') {
    return <span className="rounded bg-secondary px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">Not Confirmed</span>
  }
  if (source === 'verify' && status === 'inconclusive') {
    return <span className="rounded bg-yellow-400/10 px-1.5 py-0.5 text-[10px] font-medium text-yellow-700 dark:text-yellow-400">Inconclusive</span>
  }
  if (source === 'sniper') {
    return (
      <span className="inline-flex items-center gap-1 rounded bg-red-400/10 px-1.5 py-0.5 text-[10px] font-medium text-red-700 dark:text-red-400">
        <Crosshair className="h-3 w-3" />CVE Intel
      </span>
    )
  }
  if (source === 'deep') {
    return (
      <span className="inline-flex items-center gap-1 rounded bg-yellow-400/10 px-1.5 py-0.5 text-[10px] font-medium text-yellow-700 dark:text-yellow-400">
        <Radar className="h-3 w-3" />Deep Test
      </span>
    )
  }
  return null
}

function FilterChip({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11px] font-medium transition-colors',
        active
          ? 'border-cyber-400/40 bg-cyber-500/15 text-cyber-800 dark:text-cyber-200'
          : 'border-border bg-background text-muted-foreground hover:border-cyber-400/30 hover:text-foreground',
      )}
    >
      {children}
    </button>
  )
}
