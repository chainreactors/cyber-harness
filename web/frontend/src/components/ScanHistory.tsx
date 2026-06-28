import { Layers, ShieldAlert } from 'lucide-react'
import type { ReactNode } from 'react'
import type { ScanJob } from '../api'

interface ScanHistoryProps {
  scans: ScanJob[]
  activeId?: string
  onSelect: (scan: ScanJob) => void
  emptyMessage?: string
}

export default function ScanHistory({ scans, activeId, onSelect, emptyMessage = 'No scans yet.' }: ScanHistoryProps) {
  if (scans.length === 0) {
    return (
      <div className="text-muted-foreground/60 text-xs text-center py-6">
        {emptyMessage}
      </div>
    )
  }

  return (
    <div className="space-y-1">
      {scans.map((scan) => {
        const verifyEnabled = !!scan.verify || (!!scan.ai && !scan.sniper)
        const sniperEnabled = !!scan.sniper || (!!scan.ai && !scan.verify)
        const assetCount = scanAssetCount(scan)
        const lootCount = scanLootCount(scan)
        const hasStats = !!scan.result || assetCount > 0 || lootCount > 0
        return (
          <button
            key={scan.id}
            onClick={() => onSelect(scan)}
            title={`${scan.target} — ${scan.status}`}
            aria-current={scan.id === activeId ? 'true' : undefined}
            aria-label={`${scan.target}, ${scan.status}, ${scan.mode}, ${assetCount} assets, ${lootCount} loots`}
            className={`w-full text-left px-2.5 py-2 rounded-md transition-colors ${
              scan.id === activeId
                ? 'bg-accent border border-primary/30'
                : 'hover:bg-accent/50 border border-transparent'
            }`}
          >
            <div className="flex items-center justify-between gap-2">
              <span className="text-xs font-mono text-foreground truncate">
                {scan.target}
              </span>
              <StatusDot status={scan.status} />
            </div>
            <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5">
              <span className="text-[10px] text-muted-foreground">{scan.mode}</span>
              {hasStats && (
                <>
                  <HistoryMetric
                    icon={<Layers className="h-3 w-3" />}
                    value={assetCount}
                    label="assets"
                    className="text-primary"
                  />
                  <HistoryMetric
                    icon={<ShieldAlert className="h-3 w-3" />}
                    value={lootCount}
                    label="loots"
                    className={lootCount > 0 ? 'text-red-700 dark:text-red-300' : 'text-muted-foreground/60'}
                  />
                </>
              )}
              {verifyEnabled && <span className="text-[10px] text-primary">Verify</span>}
              {sniperEnabled && <span className="text-[10px] text-red-700 dark:text-red-300">Sniper</span>}
              {scan.deep && <span className="text-[10px] text-yellow-700 dark:text-yellow-300">Deep</span>}
              <span className="text-[10px] text-muted-foreground/60">{formatTime(scan.created_at)}</span>
            </div>
          </button>
        )
      })}
    </div>
  )
}

function HistoryMetric({
  className,
  icon,
  label,
  value,
}: {
  className: string
  icon: ReactNode
  label: string
  value: number
}) {
  return (
    <span
      title={`${value} ${label}`}
      className={`inline-flex items-center gap-0.5 text-[10px] font-medium ${className}`}
    >
      {icon}
      <span className="font-mono">{value}</span>
    </span>
  )
}

function StatusDot({ status }: { status: string }) {
  const colors: Record<string, string> = {
    queued: 'bg-gray-500',
    running: 'bg-blue-400 animate-pulse',
    completed: 'bg-green-400',
    failed: 'bg-red-400',
    canceled: 'bg-yellow-400',
  }
  return (
    <span title={status} className={`w-2 h-2 rounded-full shrink-0 ${colors[status] || colors.queued}`} />
  )
}

function scanAssetCount(scan: ScanJob) {
  return scan.result?.assets?.length || 0
}

function scanLootCount(scan: ScanJob) {
  const result = scan.result
  if (!result) {
    return 0
  }
  if (result.loots && result.loots.length > 0) {
    return result.loots.filter((loot) => loot.kind.toLowerCase() !== 'fingerprint').length
  }
  return (result.assets || []).reduce((sum, asset) => (
    sum + (asset.items || []).filter((item) => (
      item.kind === 'loot' && dataKind(item.data).toLowerCase() !== 'fingerprint'
    )).length
  ), 0)
}

function dataKind(data?: Record<string, unknown>) {
  const kind = data?.kind
  return typeof kind === 'string' ? kind : ''
}

function formatTime(iso: string): string {
  try {
    return new Date(iso).toLocaleString(undefined, {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    })
  } catch {
    return iso
  }
}
