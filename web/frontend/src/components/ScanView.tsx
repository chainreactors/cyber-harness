import { useEffect, useMemo, useState } from 'react'
import { FileText, Shield, TableProperties } from 'lucide-react'
import type { ScanJob, ScanResult } from '../api'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@aspect/ui'
import ScanProgress from './ScanProgress'
import ReportView from './ReportView'
import AssetResultView from './AssetResultView'
import FindingsPanel from './FindingsPanel'
import { buildFindings } from '../lib/scan-result'

interface ScanViewProps {
  scan: ScanJob
  lines: string[]
  report: string
  result: ScanResult | null
  logCollapsed: boolean
  onToggleLog: () => void
}

type ResultTab = 'assets' | 'findings' | 'report'

export default function ScanView({ scan, lines, report, result, logCollapsed, onToggleLog }: ScanViewProps) {
  const hasReport = !!report
  const hasResult = !!result
  const hasMarkdown = hasResult || hasReport
  const isRunning = scan.status === 'running'
  const verifyEnabled = !!scan.verify || (!!scan.ai && !scan.sniper)
  const sniperEnabled = !!scan.sniper || (!!scan.ai && !scan.verify)
  const isAIScan = verifyEnabled || sniperEnabled || !!scan.deep

  const findingsCount = useMemo(() => {
    if (!result) return 0
    return buildFindings(result).length
  }, [result])

  const hasFindings = findingsCount > 0

  const [tab, setTab] = useState<ResultTab>('assets')

  useEffect(() => {
    if (!hasResult && hasMarkdown) {
      setTab('report')
    } else if (hasResult && hasFindings && isAIScan) {
      setTab('findings')
    } else if (hasResult) {
      setTab('assets')
    }
  }, [hasMarkdown, hasResult, hasFindings, isAIScan, scan.id])

  return (
    <div className="space-y-4">
      {/* Target info */}
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-mono text-sm text-foreground">{scan.target}</span>
        <span className="text-xs text-muted-foreground px-2 py-0.5 rounded bg-secondary">{scan.mode}</span>
        {verifyEnabled && <span className="text-xs text-cyber-700 dark:text-cyber-300 px-2 py-0.5 rounded bg-cyber-500/10">Verify</span>}
        {sniperEnabled && <span className="text-xs text-red-700 dark:text-red-300 px-2 py-0.5 rounded bg-red-400/10">Sniper</span>}
        {scan.deep && <span className="text-xs text-yellow-700 dark:text-yellow-300 px-2 py-0.5 rounded bg-yellow-400/10">Deep</span>}
        <StatusIndicator status={scan.status} />
      </div>

      {/* Progress section (always shown if we have lines or are running) */}
      {(lines.length > 0 || isRunning) && (
        <ScanProgress
          lines={lines}
          status={scan.status}
          collapsed={logCollapsed}
          onToggleCollapse={onToggleLog}
        />
      )}

      {hasMarkdown && (
        <div className="space-y-3">
          <Tabs value={tab} onValueChange={(v) => setTab(v as ResultTab)}>
            <TabsList>
              <TabsTrigger value="assets">
                <TableProperties className="h-3.5 w-3.5" />
                Assets
              </TabsTrigger>
              {hasFindings && (
                <TabsTrigger value="findings">
                  <Shield className="h-3.5 w-3.5" />
                  Findings
                  <span className="ml-1 rounded-full bg-red-500/20 px-1.5 py-0.5 text-[10px] font-bold text-red-600 dark:text-red-400">
                    {findingsCount}
                  </span>
                </TabsTrigger>
              )}
              <TabsTrigger value="report">
                <FileText className="h-3.5 w-3.5" />
                Report
              </TabsTrigger>
            </TabsList>

            <TabsContent value="assets">
              {hasResult && <AssetResultView result={result} />}
            </TabsContent>
            {hasFindings && (
              <TabsContent value="findings">
                {hasResult && <FindingsPanel result={result} />}
              </TabsContent>
            )}
            <TabsContent value="report">
              <div className="animate-fade-in">
                <ReportView scan={scan} report={report} result={result} />
              </div>
            </TabsContent>
          </Tabs>
        </div>
      )}
    </div>
  )
}

function StatusIndicator({ status }: { status: string }) {
  const config: Record<string, { label: string; className: string }> = {
    queued: { label: 'Queued', className: 'text-gray-600 bg-gray-400/10 dark:text-gray-400' },
    running: { label: 'Running', className: 'text-blue-700 bg-blue-400/10 dark:text-blue-400 animate-pulse' },
    completed: { label: 'Completed', className: 'text-cyber-700 bg-cyber-400/10 dark:text-cyber-400' },
    failed: { label: 'Failed', className: 'text-red-700 bg-red-400/10 dark:text-red-400' },
    canceled: { label: 'Canceled', className: 'text-yellow-700 bg-yellow-400/10 dark:text-yellow-400' },
  }
  const { label, className } = config[status] || config.queued
  return (
    <span className={`text-[10px] font-medium px-2 py-0.5 rounded-full ${className}`}>
      {label}
    </span>
  )
}
