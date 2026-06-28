import { MarkdownContent } from '@aspect/markdown'
import type { ScanJob, ScanResult } from '../api'
import { buildMarkdownReport } from '../lib/markdown-report'

interface ReportViewProps {
  report?: string
  result?: ScanResult | null
  scan: ScanJob
}

export default function ReportView({ report = '', result, scan }: ReportViewProps) {
  const content = result ? buildMarkdownReport(scan, result) : report

  if (!content) {
    return (
      <div className="text-muted-foreground text-center py-12 text-sm">
        No report available yet.
      </div>
    )
  }

  return (
    <div className="rounded-lg border border-border bg-card/50 p-6 overflow-auto">
      <MarkdownContent content={content} className="prose-h2:mt-6" />
    </div>
  )
}
