import { useEffect, useMemo, useState } from 'react'
import { CheckCircle2, AlertTriangle, Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@cyber/ui'
import { getScan, ScanStatus, type Scan, type SCONode } from '../../api'
import { cstxFailures, listSCONodes, retryCSTXFailures, subscribeCSTXChanges, syncCSTXArtifacts } from '../../lib/cstx-runtime'
import { buildSCOModel } from '@cyber/cstx-easm'
import { buildFindingsFromSCO } from '../../lib/scan-result'
import { MarkdownContent } from '@/markdown'
import AssetResultView from '../AssetResultView'
import FindingsPanel from '../FindingsPanel'
import { buildCSTXMarkdownReport } from '../../lib/scan-report'

interface Props {
  scanID: string
  nodes?: SCONode[]
}

export default function ScanSummaryCard({ scanID, nodes: initialNodes }: Props) {
  const { t } = useTranslation('scan')
  const { t: tf, i18n } = useTranslation('findings')
  const [nodes, setNodes] = useState<SCONode[]>(initialNodes || [])
  const model = useMemo(() => buildSCOModel(nodes), [nodes])
  const findings = useMemo(() => buildFindingsFromSCO(model), [model])
  const [tab, setTab] = useState('assets')
  const [scan, setScan] = useState<Scan>()
  const [failure, setFailure] = useState('')
  const [loading, setLoading] = useState(true)
  useEffect(() => {
    let active = true
    setLoading(true)
    setScan(undefined)
    setNodes([])
    const load = async () => {
      try {
        const [record, errors, { items }] = await Promise.all([getScan(scanID), cstxFailures(scanID), listSCONodes({ scanId: scanID })])
        if (active) { setScan(record); setNodes(items); setFailure(errors.map((error) => error.error).join('; ')) }
      } catch (error) { if (active) setFailure(String(error)) }
      finally { if (active) setLoading(false) }
    }
    void syncCSTXArtifacts().then(load).catch((error) => {
      if (active) { setFailure(String(error)); setLoading(false) }
    })
    const unsubscribe = subscribeCSTXChanges(() => void load())
    return () => { active = false; unsubscribe() }
  }, [scanID])
  const complete = !loading && !failure && scan?.status === ScanStatus.COMPLETED
  const statusLabel = loading ? t('resultsLoading') : failure ? t('resultsIncomplete') : scan?.status === ScanStatus.CANCELED ? t('scanCanceled') : scan?.status === ScanStatus.FAILED ? t('scanFailed') : complete ? t('scanComplete') : t('resultsLoading')
  const lang = (i18n.resolvedLanguage || i18n.language || 'en').toLowerCase().startsWith('zh') ? 'zh' : 'en'
  const reportMd = useMemo(() => buildCSTXMarkdownReport(scanID, nodes, lang), [scanID, nodes, lang])

  return (
    <section className="overflow-hidden rounded-lg border border-border/80 bg-card">
      <header className="flex items-start justify-between gap-4 px-4 py-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : complete ? <CheckCircle2 className="h-4 w-4 text-success" /> : <AlertTriangle className="h-4 w-4 text-warning" />}
            <h3 className="text-sm font-semibold text-foreground">{statusLabel}</h3>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 pl-6 text-[11px] text-muted-foreground">
            <span>{tf('detailSummary', { targets: model.metrics.ips, services: model.metrics.ports })}</span>
            {model.metrics.urls > 0 && <MetaDivider label={`${tf('web')} ${model.metrics.urls}`} />}
            {model.metrics.vulns > 0 && <MetaDivider label={`${tf('vulns')} ${model.metrics.vulns}`} tone="error" />}
          </div>
        </div>
        {model.metrics.duration && (
          <span className="shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground">{model.metrics.duration}</span>
        )}
      </header>

      {(failure || scan?.error) && <div role="alert" className="px-4 pb-3 text-xs text-warning">{failure || scan?.error}{failure && <button className="ml-2 underline" onClick={() => void syncCSTXArtifacts().then(() => retryCSTXFailures()).catch((error) => setFailure(String(error)))}>{t('retryParsing')}</button>}</div>}
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList className="h-auto w-full justify-start rounded-none border-y border-border/60 bg-transparent px-4 py-0">
          <ResultTab value="assets">{tf('assets')}</ResultTab>
          {(
            <ResultTab value="findings">
              {tf('findings')}
              <span className="ml-1 tabular-nums text-muted-foreground">{findings.length}</span>
            </ResultTab>
          )}
          <ResultTab value="report">{tf('report')}</ResultTab>
        </TabsList>

        <TabsContent value="assets" className="mt-0 p-4 sm:p-5">
          <AssetResultView model={model} anchorPrefix={scanID} />
        </TabsContent>
        {(
          <TabsContent value="findings" className="mt-0 p-4 sm:p-5">
            <FindingsPanel findings={findings} />
          </TabsContent>
        )}
        <TabsContent value="report" className="mt-0 p-4 sm:p-5">
          <div className="prose prose-sm max-w-none dark:prose-invert">
            <MarkdownContent content={reportMd || tf('noReportAvailable')} />
          </div>
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
