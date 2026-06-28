import { useMemo, useState } from 'react'
import { X, FileText, Shield, TableProperties } from 'lucide-react'
import { Tabs, TabsList, TabsTrigger, TabsContent, Button } from '@aspect/ui'
import type { ScanResult } from '../api'
import { buildFindings } from '../lib/scan-result'
import { MarkdownContent } from '@aspect/markdown'
import AssetResultView from './AssetResultView'
import FindingsPanel from './FindingsPanel'

interface Props {
  scanID: string
  result: ScanResult | null
  report?: string
  onClose: () => void
}

type Tab = 'assets' | 'findings' | 'report'

export default function DetailPanel({ scanID, result, report, onClose }: Props) {
  const findingsCount = useMemo(() => {
    if (!result) return 0
    return buildFindings(result).length
  }, [result])

  const [tab, setTab] = useState<Tab>(findingsCount > 0 ? 'findings' : 'assets')

  return (
    <aside className="flex h-full w-full flex-col border-l border-border bg-card animate-in slide-in-from-right-2 duration-200">
      <div className="flex h-11 shrink-0 items-center justify-between border-b border-border px-3">
        <div className="flex items-center gap-2">
          <Shield className="h-3.5 w-3.5 text-primary" />
          <span className="text-xs font-medium text-foreground">Scan Details</span>
          {result && (
            <span className="text-[10px] font-mono text-muted-foreground">
              {result.summary.targets} targets / {result.summary.loots} loots
            </span>
          )}
        </div>
        <Button variant="ghost" size="icon" onClick={onClose} className="h-7 w-7 text-muted-foreground" aria-label="Close detail panel">
          <X className="h-3.5 w-3.5" />
        </Button>
      </div>

      <div className="flex-1 overflow-auto p-3">
        {result ? (
          <Tabs value={tab} onValueChange={(v: string) => setTab(v as Tab)}>
            <TabsList>
              <TabsTrigger value="assets">
                <TableProperties className="h-3.5 w-3.5" />
                Assets
              </TabsTrigger>
              {findingsCount > 0 && (
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

            <TabsContent value="assets" className="animate-in fade-in duration-150">
              <AssetResultView result={result} />
            </TabsContent>
            {findingsCount > 0 && (
              <TabsContent value="findings" className="animate-in fade-in duration-150">
                <FindingsPanel result={result} />
              </TabsContent>
            )}
            <TabsContent value="report" className="animate-in fade-in duration-150">
              <div className="prose prose-sm dark:prose-invert max-w-none">
                <MarkdownContent content={report || 'No report available.'} />
              </div>
            </TabsContent>
          </Tabs>
        ) : (
          <div className="flex items-center justify-center py-10 text-xs text-muted-foreground">
            No results available
          </div>
        )}
      </div>
    </aside>
  )
}
