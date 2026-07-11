import { useEffect, useMemo, useState } from 'react'
import { CheckCircle2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@cyber/ui'
import { fetchScanReport, type ScanResult } from '../../api'
import { buildFindings } from '../../lib/scan-result'
import { MarkdownContent } from '@/markdown'
import AssetResultView from '../AssetResultView'
import FindingsPanel from '../FindingsPanel'

interface Props {
  scanID: string
  result: ScanResult
}

export default function ScanSummaryCard({ scanID, result }: Props) {
  const { t } = useTranslation('scan')
  const { t: tf, i18n } = useTranslation('findings')
  const findingsCount = useMemo(() => buildFindings(result).length, [result])
  const [tab, setTab] = useState('assets')
  const [reportMd, setReportMd] = useState('')
  const [reportLoadedKey, setReportLoadedKey] = useState('')
  const s = result.summary
  const lang = (i18n.resolvedLanguage || i18n.language || 'en').toLowerCase().startsWith('zh') ? 'zh' : 'en'
  const reportKey = `${scanID}:${lang}`

  useEffect(() => {
    if (tab !== 'report' || reportLoadedKey === reportKey) return
    setReportMd('')
    let cancelled = false
    fetchScanReport(scanID, lang)
      .then((md) => { if (!cancelled) { setReportMd(md); setReportLoadedKey(reportKey) } })
      .catch(() => { if (!cancelled) { setReportMd(''); setReportLoadedKey(reportKey) } })
    return () => { cancelled = true }
  }, [tab, scanID, lang, reportKey, reportLoadedKey])

  const reportLoading = tab === 'report' && reportLoadedKey !== reportKey

  return (
    <section className="overflow-hidden rounded-lg border border-border/80 bg-card">
      <header className="flex items-start justify-between gap-4 px-4 py-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <CheckCircle2 aria-hidden="true" className="h-4 w-4 shrink-0 text-success" />
            <h3 className="text-sm font-semibold text-foreground">{t('scanComplete')}</h3>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 pl-6 text-[11px] text-muted-foreground">
            <span>{tf('detailSummary', { targets: s.targets, services: s.services })}</span>
            {s.webs > 0 && <MetaDivider label={`${tf('web')} ${s.webs}`} />}
            {s.probes > 0 && <MetaDivider label={`${tf('probes')} ${s.probes}`} />}
            {s.errors > 0 && <MetaDivider label={`${tf('errors')} ${s.errors}`} tone="error" />}
          </div>
        </div>
        {s.duration && (
          <span className="shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground">{s.duration}</span>
        )}
      </header>

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList className="h-auto w-full justify-start rounded-none border-y border-border/60 bg-transparent px-4 py-0">
          <ResultTab value="assets">{tf('assets')}</ResultTab>
          {findingsCount > 0 && (
            <ResultTab value="findings">
              {tf('findings')}
              <span className="ml-1 tabular-nums text-muted-foreground">{findingsCount}</span>
            </ResultTab>
          )}
          <ResultTab value="report">{tf('report')}</ResultTab>
        </TabsList>

        <TabsContent value="assets" className="mt-0 p-4 sm:p-5">
          <AssetResultView result={result} anchorPrefix={scanID} />
        </TabsContent>
        {findingsCount > 0 && (
          <TabsContent value="findings" className="mt-0 p-4 sm:p-5">
            <FindingsPanel result={result} />
          </TabsContent>
        )}
        <TabsContent value="report" className="mt-0 p-4 sm:p-5">
          {reportLoading ? (
            <div role="status" className="py-4 text-sm text-muted-foreground">{tf('loadingReport')}</div>
          ) : (
            <div className="prose prose-sm max-w-none dark:prose-invert">
              <MarkdownContent content={reportMd || tf('noReportAvailable')} />
            </div>
          )}
        </TabsContent>
      </Tabs>
    </section>
  )
}

function MetaDivider({ label, tone = 'muted' }: { label: string; tone?: 'muted' | 'error' }) {
  return (
    <span className={tone === 'error' ? 'border-l border-border pl-2 text-destructive' : 'border-l border-border pl-2'}>{label}</span>
  )
}

function ResultTab({ value, children }: { value: string; children: React.ReactNode }) {
  return (
    <TabsTrigger
      value={value}
      className="-mb-px mr-5 h-9 rounded-none border-b-2 border-transparent bg-transparent px-0 py-0 text-xs shadow-none last:mr-0 data-[state=active]:border-primary data-[state=active]:bg-transparent data-[state=active]:text-foreground data-[state=active]:shadow-none"
    >
      {children}
    </TabsTrigger>
  )
}
