import { useMemo, useState, type ReactNode } from 'react'
import {
  AlertTriangle,
  CheckCircle2,
  Circle,
  History,
  Info,
  Layers,
  RefreshCw,
  Search,
  Shield,
  ShieldAlert,
  X,
} from 'lucide-react'
import type { ScanJob, ScanOptions, ScanResult } from '../api'
import ScanForm from './ScanForm'
import ScanView from './ScanView'
import { cn } from '@aspect/theme'
import { Input, Tooltip, TooltipContent, TooltipTrigger } from '@aspect/ui'
import { DetailGroup, DetailRow, formatDateTime } from '@aspect/terminal'

interface ScanWorkspaceProps {
  scans: ScanJob[]
  activeScan: ScanJob | null
  lines: string[]
  report: string
  result: ScanResult | null
  scanning: boolean
  error: string
  logCollapsed: boolean
  analysisAvailable: boolean
  status?: ReactNode
  actions?: ReactNode
  onSubmit: (target: string, mode: string, options: ScanOptions) => void
  onSelectScan: (scan: ScanJob) => void
  onRefreshScans: () => void
  onToggleLog: () => void
  onClearError: () => void
}

export default function ScanWorkspace({
  actions,
  activeScan,
  analysisAvailable,
  error,
  lines,
  logCollapsed,
  onClearError,
  onRefreshScans,
  onSelectScan,
  onSubmit,
  onToggleLog,
  report,
  result,
  scanning,
  scans,
  status,
}: ScanWorkspaceProps) {
  const [detailsOpen, setDetailsOpen] = useState(false)
  const title = activeScan?.target || 'Scan Console'
  const scanStatus = activeScan?.status || (scanning ? 'running' : 'ready')
  const running = scans.filter((scan) => scan.status === 'queued' || scan.status === 'running').length
  const completed = scans.filter((scan) => scan.status === 'completed').length

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col bg-card">
      <ScanHeader
        status={scanStatus}
        title={title}
        actions={
          <>
            {status}
            {actions}
            <IconButton label="Refresh scans" onClick={onRefreshScans}>
              <RefreshCw className="h-3.5 w-3.5" />
            </IconButton>
            <IconButton
              active={detailsOpen}
              disabled={!activeScan}
              label={detailsOpen ? 'Hide details' : 'Show details'}
              onClick={() => setDetailsOpen((value) => !value)}
            >
              <Info className="h-3.5 w-3.5" />
            </IconButton>
          </>
        }
      />

      <div className="shrink-0 border-b border-border bg-card/85 px-3 py-2">
        <ScanForm
          onSubmit={onSubmit}
          disabled={scanning}
          analysisAvailable={analysisAvailable}
        />
      </div>

      {error && (
        <div
          role="alert"
          className="mx-3 mt-3 flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive"
        >
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          <span className="min-w-0 flex-1 break-words">{error}</span>
          <button
            type="button"
            aria-label="Dismiss error"
            onClick={onClearError}
            className="rounded p-0.5 text-destructive/70 hover:bg-destructive/10 hover:text-destructive"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      )}

      <div className="flex min-h-0 min-w-0 flex-1 flex-col lg:flex-row">
        <ScanNavigator
          activeID={activeScan?.id}
          completed={completed}
          running={running}
          scans={scans}
          onSelect={onSelectScan}
        />

        <section className="flex min-h-0 min-w-0 flex-1 flex-col">
          {activeScan ? (
            <div className="min-h-0 flex-1 overflow-auto p-3">
              <ScanView
                scan={activeScan}
                lines={lines}
                report={report}
                result={result}
                logCollapsed={logCollapsed}
                onToggleLog={onToggleLog}
              />
            </div>
          ) : (
            <EmptyScanConsole
              analysisAvailable={analysisAvailable}
              completed={completed}
              running={running}
              total={scans.length}
            />
          )}
        </section>

        {detailsOpen && activeScan && (
          <ScanDetails
            lines={lines.length}
            result={result || activeScan.result || null}
            scan={activeScan}
            onClose={() => setDetailsOpen(false)}
          />
        )}
      </div>
    </div>
  )
}

function ScanHeader({ actions, status, title }: { actions?: ReactNode; status: string; title: string }) {
  return (
    <div className="flex h-11 min-w-0 shrink-0 items-center justify-between border-b border-border px-3">
      <div className="flex min-w-0 items-center gap-2" title={title}>
        <Shield className="h-4 w-4 shrink-0 text-primary" />
        <span className="truncate text-sm font-medium text-foreground">{title}</span>
        <span className={cn('shrink-0 rounded px-1.5 py-0.5 text-[10px]', scanStatusColor(status))}>
          {scanStatusLabel(status)}
        </span>
      </div>
      {actions && <div className="flex min-w-0 items-center gap-1">{actions}</div>}
    </div>
  )
}

function ScanNavigator({
  activeID,
  completed,
  onSelect,
  running,
  scans,
}: {
  activeID?: string
  completed: number
  running: number
  scans: ScanJob[]
  onSelect: (scan: ScanJob) => void
}) {
  const [query, setQuery] = useState('')
  const filteredScans = useMemo(() => {
    const normalized = query.trim().toLowerCase()
    if (!normalized) return scans
    return scans.filter((scan) => scan.target.toLowerCase().includes(normalized))
  }, [query, scans])

  return (
    <aside className="flex max-h-72 w-full shrink-0 flex-col border-b border-border lg:max-h-none lg:w-64 lg:border-b-0 lg:border-r">
      <div className="border-b border-border p-2">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search targets"
            aria-label="Search scan targets"
            className="h-8 pl-8 pr-8 text-xs"
          />
          {query && (
            <button
              type="button"
              aria-label="Clear target search"
              onClick={() => setQuery('')}
              className="absolute right-1.5 top-1/2 inline-flex h-5 w-5 -translate-y-1/2 items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
            >
              <X className="h-3 w-3" />
            </button>
          )}
        </div>
      </div>
      <div className="flex h-9 shrink-0 items-center justify-between gap-2 border-b border-border px-3 text-[10px] uppercase text-muted-foreground">
        <span>History</span>
        <span className="truncate">
          {running} running{completed ? ` · ${completed} done` : ''}
        </span>
      </div>
      <div className="min-h-0 flex-1 overflow-auto p-2">
        {filteredScans.length === 0 ? (
          <div className="px-2 py-3 text-xs text-muted-foreground">
            {query.trim() ? 'No matching targets.' : 'No scans yet.'}
          </div>
        ) : (
          filteredScans.map((scan) => (
            <ScanNavButton
              key={scan.id}
              active={scan.id === activeID}
              scan={scan}
              onClick={() => onSelect(scan)}
            />
          ))
        )}
      </div>
    </aside>
  )
}

function ScanNavButton({ active, onClick, scan }: { active: boolean; onClick: () => void; scan: ScanJob }) {
  const assets = scan.result?.assets?.length || 0
  const loots = scanLootCount(scan.result)
  const badges = scanBadges(scan)

  return (
    <button
      type="button"
      onClick={onClick}
      title={scanDetails(scan)}
      aria-current={active ? 'true' : undefined}
      className={cn(
        'mb-1 flex w-full items-start gap-2 rounded-md px-2 py-2 text-left text-xs transition-colors',
        active ? 'bg-primary/10 text-foreground' : 'text-muted-foreground hover:bg-accent hover:text-foreground',
      )}
    >
      <Circle className={cn('mt-1 h-2.5 w-2.5 shrink-0 fill-current', scanStateColor(scan.status))} />
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="min-w-0 flex-1 truncate font-mono font-medium">{scan.target}</span>
          <span className={cn('shrink-0 text-[10px]', scanStateTextColor(scan.status))}>
            {scanStatusLabel(scan.status)}
          </span>
        </span>
        <span className="mt-0.5 block truncate font-mono">{scan.mode}</span>
        <span className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-[10px]">
          {assets > 0 && (
            <span className="inline-flex items-center gap-0.5 text-primary">
              <Layers className="h-3 w-3" />
              <span className="font-mono">{assets}</span>
            </span>
          )}
          {loots > 0 && (
            <span className="inline-flex items-center gap-0.5 text-red-700 dark:text-red-300">
              <ShieldAlert className="h-3 w-3" />
              <span className="font-mono">{loots}</span>
            </span>
          )}
          {badges.map((badge) => (
            <span key={badge} className="text-muted-foreground/80">{badge}</span>
          ))}
          <span className="truncate text-muted-foreground/60">{shortTime(scan.created_at)}</span>
        </span>
      </span>
    </button>
  )
}

function EmptyScanConsole({
  analysisAvailable,
  completed,
  running,
  total,
}: {
  analysisAvailable: boolean
  completed: number
  running: number
  total: number
}) {
  return (
    <div className="flex min-h-0 flex-1 items-center justify-center bg-[#060a0d] p-6">
      <div className="space-y-4 text-center">
        <Shield className="mx-auto h-14 w-14 text-primary/20" strokeWidth={1.25} />
        <div className="space-y-1">
          <p className="text-sm font-medium text-slate-100">No active scan</p>
          <p className="text-xs text-slate-500">Ready</p>
        </div>
        <div className="flex flex-wrap justify-center gap-2">
          <Metric icon={<History className="h-3.5 w-3.5" />} label="History" value={total} />
          <Metric icon={<Circle className="h-3.5 w-3.5 fill-current" />} label="Running" value={running} tone={running ? 'ready' : 'muted'} />
          <Metric icon={<CheckCircle2 className="h-3.5 w-3.5" />} label="Completed" value={completed} />
          <Metric
            icon={analysisAvailable ? <CheckCircle2 className="h-3.5 w-3.5" /> : <AlertTriangle className="h-3.5 w-3.5" />}
            label="LLM"
            value={analysisAvailable ? 'Ready' : 'Offline'}
            tone={analysisAvailable ? 'ready' : 'warning'}
          />
        </div>
      </div>
    </div>
  )
}

function ScanDetails({
  lines,
  onClose,
  result,
  scan,
}: {
  lines: number
  onClose: () => void
  result: ScanResult | null
  scan: ScanJob
}) {
  return (
    <aside className="flex max-h-72 w-full shrink-0 flex-col border-t border-border bg-card lg:max-h-none lg:w-80 lg:border-l lg:border-t-0">
      <div className="flex h-10 shrink-0 items-center justify-between border-b border-border px-3">
        <span className="text-xs font-medium uppercase text-muted-foreground">Details</span>
        <IconButton label="Close details" onClick={onClose}>
          <X className="h-3.5 w-3.5" />
        </IconButton>
      </div>
      <div className="min-h-0 flex-1 overflow-auto p-3 text-xs">
        <DetailGroup title="Scan">
          <DetailRow label="Target" value={scan.target} mono />
          <DetailRow label="ID" value={scan.id} mono />
          <DetailRow label="State" value={scanStatusLabel(scan.status)} />
          <DetailRow label="Mode" value={scan.mode} />
          <DetailRow label="Created" value={formatDateTime(scan.created_at)} />
          <DetailRow label="Updated" value={formatDateTime(scan.updated_at)} />
          <DetailRow label="Output" value={lines ? `${lines} lines` : undefined} />
        </DetailGroup>
        <DetailGroup title="Options">
          <DetailRow label="Verify" value={scan.verify ? 'enabled' : undefined} />
          <DetailRow label="Sniper" value={scan.sniper ? 'enabled' : undefined} />
          <DetailRow label="Deep" value={scan.deep ? 'enabled' : undefined} />
          <DetailRow label="AI" value={scan.ai ? 'enabled' : undefined} />
        </DetailGroup>
        <DetailGroup title="Result">
          <DetailRow label="Assets" value={result?.assets?.length} />
          <DetailRow label="Loots" value={scanLootCount(result)} />
          <DetailRow label="Services" value={result?.summary?.services} />
          <DetailRow label="Webs" value={result?.summary?.webs} />
          <DetailRow label="Requests" value={result?.summary?.requests} />
          <DetailRow label="Duration" value={result?.summary?.duration} />
        </DetailGroup>
      </div>
    </aside>
  )
}

function IconButton({
  active,
  children,
  disabled,
  label,
  onClick,
}: {
  active?: boolean
  children: ReactNode
  disabled?: boolean
  label: string
  onClick: () => void
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label={label}
          title={label}
          disabled={disabled}
          onClick={onClick}
          className={cn(
            'inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-40',
            active && 'bg-primary/10 text-primary',
          )}
        >
          {children}
        </button>
      </TooltipTrigger>
      <TooltipContent side="bottom">{label}</TooltipContent>
    </Tooltip>
  )
}

function Metric({
  icon,
  label,
  tone = 'muted',
  value,
}: {
  icon: ReactNode
  label: string
  tone?: 'muted' | 'ready' | 'warning'
  value: ReactNode
}) {
  return (
    <div
      className={cn(
        'inline-flex items-center gap-2 rounded-md border px-2.5 py-1.5 text-xs',
        tone === 'ready' && 'border-primary/25 bg-primary/10 text-primary',
        tone === 'warning' && 'border-yellow-400/25 bg-yellow-400/10 text-yellow-300',
        tone === 'muted' && 'border-slate-800 bg-slate-900/70 text-slate-400',
      )}
    >
      {icon}
      <span className="text-slate-500">{label}</span>
      <span className="font-mono text-slate-100">{value}</span>
    </div>
  )
}

function scanDetails(scan: ScanJob) {
  return [
    `target: ${scan.target}`,
    `id: ${scan.id}`,
    `state: ${scanStatusLabel(scan.status)}`,
    `mode: ${scan.mode}`,
    scan.created_at ? `created: ${formatDateTime(scan.created_at)}` : '',
    scan.updated_at ? `updated: ${formatDateTime(scan.updated_at)}` : '',
  ].filter(Boolean).join('\n')
}

function scanBadges(scan: ScanJob) {
  const badges: string[] = []
  if (scan.verify || (scan.ai && !scan.sniper)) badges.push('Verify')
  if (scan.sniper || (scan.ai && !scan.verify)) badges.push('Sniper')
  if (scan.deep) badges.push('Deep')
  return badges
}

function scanLootCount(result?: ScanResult | null) {
  if (!result) return 0
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

function shortTime(value: string) {
  try {
    return new Date(value).toLocaleString(undefined, {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    })
  } catch {
    return value
  }
}

function scanStatusLabel(status: string) {
  switch (status) {
    case 'queued':
      return 'queued'
    case 'running':
      return 'running'
    case 'completed':
      return 'done'
    case 'failed':
      return 'failed'
    case 'canceled':
      return 'canceled'
    case 'ready':
      return 'ready'
    default:
      return status || 'ready'
  }
}

function scanStatusColor(status: string) {
  switch (status) {
    case 'running':
    case 'queued':
      return 'bg-primary/10 text-primary'
    case 'completed':
      return 'bg-muted text-muted-foreground'
    case 'failed':
      return 'bg-destructive/10 text-destructive'
    case 'canceled':
      return 'bg-yellow-400/10 text-yellow-700 dark:text-yellow-300'
    default:
      return 'bg-muted text-muted-foreground'
  }
}

function scanStateColor(status: string) {
  switch (status) {
    case 'running':
    case 'queued':
      return 'text-primary'
    case 'completed':
      return 'text-muted-foreground'
    case 'failed':
      return 'text-destructive'
    case 'canceled':
      return 'text-warning'
    default:
      return 'text-muted-foreground'
  }
}

function scanStateTextColor(status: string) {
  switch (status) {
    case 'running':
    case 'queued':
      return 'text-primary'
    case 'failed':
      return 'text-destructive'
    case 'canceled':
      return 'text-yellow-700 dark:text-yellow-300'
    default:
      return 'text-muted-foreground'
  }
}
