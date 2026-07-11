import { AlertTriangle, CheckCircle2, HelpCircle, Info, ShieldCheck, XCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { ScanResult } from '../api'
import { buildFindingsSummary, PRIORITY_ORDER, type FindingsSummaryModel } from '../lib/scan-result'
import { severityTone } from '../lib/tones'
import { useMemo } from 'react'
import { cn } from '@aspect/theme'
import { Badge, Card, Meter } from '@aspect/ui'

interface FindingsSummaryProps {
  result: ScanResult
}

export default function FindingsSummary({ result }: FindingsSummaryProps) {
  const { t } = useTranslation('findings')
  const summary = useMemo(() => buildFindingsSummary(result), [result])

  if (!summary) return null

  return (
    <Card className="bg-card/50 p-4 space-y-4">
      <div className="flex items-center gap-2 text-sm font-medium text-ai">
        <ShieldCheck className="h-4 w-4" />
        <span>{t('aiAnalysisSummary')}</span>
      </div>

      <PriorityGrid summary={summary} />

      {Object.keys(summary.byStatus).length > 0 && (
        <VerificationStats summary={summary} />
      )}

      {summary.topFinding && (
        <TopFinding summary={summary} />
      )}
    </Card>
  )
}

function PriorityGrid({ summary }: { summary: FindingsSummaryModel }) {
  const { t } = useTranslation('findings')
  return (
    <div className="grid grid-cols-3 gap-2 sm:grid-cols-5">
      {PRIORITY_ORDER.map((priority) => {
        const tone = severityTone[priority]
        const count = summary.byPriority[priority]?.length || 0
        return (
          <div
            key={priority}
            className={cn(
              'rounded-md border p-2.5 text-center',
              tone.bg, tone.border,
              count === 0 && 'opacity-40',
            )}
          >
            <div className={cn('text-lg font-bold tabular-nums', tone.text)}>{count}</div>
            <div className="mono-label text-muted-foreground">{t(`severity_${priority}`)}</div>
          </div>
        )
      })}
    </div>
  )
}

function VerificationStats({ summary }: { summary: FindingsSummaryModel }) {
  const { t } = useTranslation('findings')
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
        <span className="text-muted-foreground">{t('aiVerification')}</span>
        <span className="font-medium text-foreground">
          {t('confirmedRatio', { confirmed, total })}
        </span>
      </div>

      <Meter value={ratio} tone="success" />

      <div className="flex flex-wrap gap-3 text-[11px]">
        {confirmed > 0 && (
          <span className="inline-flex items-center gap-1 text-success">
            <CheckCircle2 className="h-3 w-3" />{t('confirmedCount', { count: confirmed })}
          </span>
        )}
        {info > 0 && (
          <span className="inline-flex items-center gap-1 text-info">
            <Info className="h-3 w-3" />{t('infoCount', { count: info })}
          </span>
        )}
        {inconclusive > 0 && (
          <span className="inline-flex items-center gap-1 text-warning">
            <HelpCircle className="h-3 w-3" />{t('inconclusiveCount', { count: inconclusive })}
          </span>
        )}
        {notConfirmed > 0 && (
          <span className="inline-flex items-center gap-1 text-muted-foreground">
            <XCircle className="h-3 w-3" />{t('notConfirmedCount', { count: notConfirmed })}
          </span>
        )}
      </div>
    </div>
  )
}

function TopFinding({ summary }: { summary: FindingsSummaryModel }) {
  const { t } = useTranslation('findings')
  const top = summary.topFinding!
  const tone = severityTone[top.priority]

  return (
    <div className={cn('rounded-md border p-3', tone.border, tone.bg)}>
      <div className="flex items-start gap-2">
        <AlertTriangle className={cn('h-4 w-4 mt-0.5 shrink-0', tone.text)} />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className={cn('mono-label', tone.text)}>{t(`severity_${top.priority}`)}</span>
            <span className="text-xs font-medium text-foreground break-words">{top.title}</span>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
            <span className="font-mono break-all">{top.target}</span>
            {top.source === 'verify' && top.status === 'confirmed' && (
              <span className="inline-flex items-center gap-1 text-success">
                <CheckCircle2 className="h-3 w-3" />{t('aiVerified')}
              </span>
            )}
            {top.tags.slice(0, 3).map(tag => (
              <Badge key={tag} variant="muted" size="sm" className="bg-background/50">{tag}</Badge>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}
