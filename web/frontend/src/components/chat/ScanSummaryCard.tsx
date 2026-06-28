import { ArrowRight, Shield, Server, Bug, FileText } from 'lucide-react'
import type { ScanResult } from '../../api'
import { cn } from '@aspect/theme'

interface Props {
  scanID: string
  result: ScanResult
  onViewDetails: (scanID: string) => void
}

export default function ScanSummaryCard({ scanID, result, onViewDetails }: Props) {
  const s = result.summary

  return (
    <div className="rounded-lg border border-primary/30 bg-primary/5 overflow-hidden">
      <div className="px-3 py-2 flex items-center gap-2 border-b border-primary/20">
        <Shield className="h-3.5 w-3.5 text-primary" />
        <span className="text-xs font-medium text-primary">Scan Complete</span>
        {s.duration && (
          <span className="ml-auto text-[10px] font-mono text-muted-foreground">{s.duration}</span>
        )}
      </div>
      <div className="flex flex-wrap gap-3 px-3 py-2">
        <Metric icon={<Server className="h-3 w-3" />} label="Assets" value={s.targets} />
        <Metric icon={<FileText className="h-3 w-3" />} label="Services" value={s.services} />
        <Metric icon={<Bug className="h-3 w-3" />} label="Loots" value={s.loots} tone={s.loots > 0 ? 'warn' : 'muted'} />
        {s.errors > 0 && <Metric icon={<Bug className="h-3 w-3" />} label="Errors" value={s.errors} tone="error" />}
      </div>
      <button
        type="button"
        onClick={() => onViewDetails(scanID)}
        className={cn(
          'flex w-full items-center justify-center gap-1.5 border-t border-primary/20',
          'px-3 py-1.5 text-xs font-medium text-primary',
          'hover:bg-primary/10 transition-colors',
        )}
      >
        View Details
        <ArrowRight className="h-3 w-3" />
      </button>
    </div>
  )
}

function Metric({
  icon,
  label,
  value,
  tone = 'muted',
}: {
  icon: React.ReactNode
  label: string
  value: number
  tone?: 'muted' | 'warn' | 'error'
}) {
  return (
    <div
      className={cn(
        'flex items-center gap-1.5 text-xs',
        tone === 'warn' && 'text-yellow-600 dark:text-warning',
        tone === 'error' && 'text-red-600 dark:text-red-400',
        tone === 'muted' && 'text-muted-foreground',
      )}
    >
      {icon}
      <span>{label}</span>
      <span className="font-mono font-bold text-foreground">{value}</span>
    </div>
  )
}
