import { AlertTriangle, CheckCircle2, HelpCircle, Info, ShieldCheck, XCircle } from 'lucide-react'
import type { ScanResult } from '../api'
import { buildFindingsSummary, PRIORITY_ORDER, type FindingsSummaryModel } from '../lib/scan-result'
import { useMemo } from 'react'
import { cn } from '@/lib/utils'

interface FindingsSummaryProps {
  result: ScanResult
}

const PRIORITY_CONFIG = {
  critical: { label: 'Critical', bg: 'bg-red-500/15', text: 'text-red-600 dark:text-red-400', border: 'border-red-500/30' },
  high: { label: 'High', bg: 'bg-orange-500/15', text: 'text-orange-600 dark:text-orange-400', border: 'border-orange-500/30' },
  medium: { label: 'Medium', bg: 'bg-yellow-500/15', text: 'text-yellow-600 dark:text-yellow-400', border: 'border-yellow-500/30' },
  low: { label: 'Low', bg: 'bg-green-500/15', text: 'text-green-600 dark:text-green-400', border: 'border-green-500/30' },
  info: { label: 'Info', bg: 'bg-blue-500/15', text: 'text-blue-600 dark:text-blue-400', border: 'border-blue-500/30' },
} as const

export default function FindingsSummary({ result }: FindingsSummaryProps) {
  const summary = useMemo(() => buildFindingsSummary(result), [result])

  if (!summary) return null

  return (
    <div className="rounded-lg border border-border bg-card/50 p-4 space-y-4">
      <div className="flex items-center gap-2 text-sm font-medium text-cyber-700 dark:text-cyber-400">
        <ShieldCheck className="h-4 w-4" />
        <span>AI Analysis Summary</span>
      </div>

      <PriorityGrid summary={summary} />

      {Object.keys(summary.byStatus).length > 0 && (
        <VerificationStats summary={summary} />
      )}

      {summary.topFinding && (
        <TopFinding summary={summary} />
      )}
    </div>
  )
}

function PriorityGrid({ summary }: { summary: FindingsSummaryModel }) {
  return (
    <div className="grid grid-cols-5 gap-2">
      {PRIORITY_ORDER.map((priority) => {
        const config = PRIORITY_CONFIG[priority]
        const count = summary.byPriority[priority]?.length || 0
        return (
          <div
            key={priority}
            className={cn(
              'rounded-md border p-2.5 text-center',
              config.bg, config.border,
              count === 0 && 'opacity-40',
            )}
          >
            <div className={cn('text-lg font-bold tabular-nums', config.text)}>{count}</div>
            <div className="text-[10px] uppercase text-muted-foreground">{config.label}</div>
          </div>
        )
      })}
    </div>
  )
}

function VerificationStats({ summary }: { summary: FindingsSummaryModel }) {
  const confirmed = summary.byStatus['confirmed']?.length || 0
  const info = summary.byStatus['info']?.length || 0
  const inconclusive = summary.byStatus['inconclusive']?.length || 0
  const notConfirmed = summary.byStatus['not_confirmed']?.length || 0
  const total = confirmed + info + inconclusive + notConfirmed

  if (total === 0) return null

  const ratio = total > 0 ? (confirmed / total) * 100 : 0

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between text-xs">
        <span className="text-muted-foreground">AI Verification</span>
        <span className="font-medium text-foreground">
          {confirmed}/{total} confirmed
        </span>
      </div>

      <div className="h-2 w-full overflow-hidden rounded-full bg-secondary">
        <div
          className="h-full rounded-full bg-green-500 transition-all"
          style={{ width: `${ratio}%` }}
        />
      </div>

      <div className="flex flex-wrap gap-3 text-[11px]">
        {confirmed > 0 && (
          <span className="inline-flex items-center gap-1 text-green-600 dark:text-green-400">
            <CheckCircle2 className="h-3 w-3" />{confirmed} confirmed
          </span>
        )}
        {info > 0 && (
          <span className="inline-flex items-center gap-1 text-blue-600 dark:text-blue-400">
            <Info className="h-3 w-3" />{info} info
          </span>
        )}
        {inconclusive > 0 && (
          <span className="inline-flex items-center gap-1 text-yellow-600 dark:text-yellow-400">
            <HelpCircle className="h-3 w-3" />{inconclusive} inconclusive
          </span>
        )}
        {notConfirmed > 0 && (
          <span className="inline-flex items-center gap-1 text-muted-foreground">
            <XCircle className="h-3 w-3" />{notConfirmed} not confirmed
          </span>
        )}
      </div>
    </div>
  )
}

function TopFinding({ summary }: { summary: FindingsSummaryModel }) {
  const top = summary.topFinding!
  const config = PRIORITY_CONFIG[top.priority]

  return (
    <div className={cn('rounded-md border p-3', config.border, config.bg)}>
      <div className="flex items-start gap-2">
        <AlertTriangle className={cn('h-4 w-4 mt-0.5 shrink-0', config.text)} />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className={cn('text-xs font-semibold uppercase', config.text)}>{top.priority}</span>
            <span className="text-xs font-medium text-foreground break-words">{top.title}</span>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
            <span className="font-mono break-all">{top.target}</span>
            {top.source === 'verify' && top.status === 'confirmed' && (
              <span className="inline-flex items-center gap-1 text-green-600 dark:text-green-400">
                <CheckCircle2 className="h-3 w-3" />AI Verified
              </span>
            )}
            {top.tags.slice(0, 3).map(tag => (
              <span key={tag} className="rounded bg-background/50 px-1.5 py-0.5 text-[10px]">{tag}</span>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}
