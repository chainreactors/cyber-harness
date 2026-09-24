import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, CircleX, Loader2, Wrench } from 'lucide-react'
import { buildSCOModel, type SCONode } from '@cyber/cstx-easm'
import { Badge, DisclosureCard, Tabs, TabsContent, TabsList, TabsTrigger } from '@cyber/ui'
import { cn } from '@cyber/theme'
import { ToolCallDisplay, formatArgs, stripAnsiControl, summarizeArgs } from '@/viewer'
import { cstxFailures, listSCONodes, retryCSTXFailures, subscribeCSTXChanges, syncCSTXArtifacts } from '../../lib/cstx-runtime'
import { buildFindingsFromSCO } from '../../lib/scan-result'
import AssetResultView from '../AssetResultView'
import FindingsPanel from '../FindingsPanel'

export interface ScannerToolCallProps {
  id: string
  toolName: string
  toolArgs?: string
  result?: string
  pending?: boolean
  error?: boolean
}

export default function ScannerToolCall({
  id,
  toolName,
  toolArgs = '',
  result,
  pending = false,
  error = false,
}: ScannerToolCallProps) {
  const { t } = useTranslation('scan')
  const { t: tChat } = useTranslation('chat')
  const { t: tf } = useTranslation('findings')
  const [nodes, setNodes] = useState<SCONode[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [failure, setFailure] = useState('')
  const model = useMemo(() => buildSCOModel(nodes || []), [nodes])
  const findings = useMemo(() => buildFindingsFromSCO(model), [model])

  useEffect(() => {
    if (!id) {
      setNodes(null)
      return
    }
    let disposed = false
    const load = () => {
      setLoading(true)
      return Promise.all([listSCONodes({ scanId: id }), cstxFailures(id)]).then(([{ items }, errors]) => {
        if (!disposed) { setNodes(items); setFailure(errors.map((error) => error.error).join('; ')) }
      }).catch((error) => {
        if (!disposed) setFailure(String(error))
      }).finally(() => {
        if (!disposed) setLoading(false)
      })
    }
    const unsubscribe = subscribeCSTXChanges(() => void load())
    void syncCSTXArtifacts().then(load).catch((error) => {
      if (!disposed) { setFailure(String(error)); setLoading(false) }
    })
    return () => {
      disposed = true
      unsubscribe()
    }
  }, [error, id, pending])

  const labels = {
    arguments: tChat('toolCard.arguments'),
    result: tChat('toolCard.result'),
    failed: tChat('toolCard.failed'),
    running: tChat('toolCard.running'),
    completed: tChat('toolCard.completed'),
  }
  const failureNotice = failure && (
    <div role="alert" className="px-3 py-2 text-xs text-warning">
      {t('resultsIncomplete')}: {failure}
      <button className="ml-2 underline" onClick={() => void syncCSTXArtifacts().then(() => retryCSTXFailures()).catch((error) => setFailure(String(error)))}>{t('retryParsing')}</button>
    </div>
  )

  if (!nodes || nodes.length === 0) {
    return (
      <div>
      <ToolCallDisplay
        toolName={toolName}
        toolArgs={toolArgs}
        result={result}
        pending={pending}
        error={error}
        labels={labels}
      />
      {failureNotice}
      </div>
    )
  }

  const summary = summarizeArgs(toolArgs)
  const formattedArgs = formatArgs(toolArgs)
  const displayResult = result === undefined ? undefined : stripAnsiControl(result)

  return (
    <DisclosureCard
      animated
      // Collapsed by default (even once complete): a finished scan otherwise
      // mounts a tall EASM table + raw-output block that buries the agent's
      // written report — the operator can expand on demand. The result count in
      // the header keeps a collapsed card informative.
      defaultExpanded={false}
      className={cn(
        'transition-colors duration-200',
        error ? 'border-destructive/35' : pending ? 'border-warning/30' : 'border-border',
      )}
      header={
        <>
          <Wrench className={cn('h-3.5 w-3.5 shrink-0', error ? 'text-destructive' : pending ? 'text-warning' : 'text-muted-foreground')} />
          <Badge variant="outline" size="sm" className="shrink-0 bg-muted/40 font-mono font-medium text-foreground">
            {toolName}
          </Badge>
          <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground" title={summary || formattedArgs}>
            {summary || (error ? labels.failed : pending ? labels.running : labels.completed)}
          </span>
          {nodes && nodes.length > 0 && (
            <Badge variant="muted" size="sm" className="shrink-0 rounded-full font-mono tabular-nums">
              {nodes.length} {t('assets')}
            </Badge>
          )}
          {error
            ? <CircleX className="h-3 w-3 shrink-0 text-destructive" />
            : pending
            ? <Loader2 className="h-3 w-3 shrink-0 animate-spin text-warning" />
            : <Check className="h-3 w-3 shrink-0 text-success" />}
        </>
      }
    >
      <div className="border-t border-border">
        {failureNotice}
        {nodes && nodes.length > 0 && (
          <Tabs defaultValue="assets" className="p-3">
            <TabsList>
              <TabsTrigger value="assets">{tf('assets')}</TabsTrigger>
              <TabsTrigger value="findings">{tf('findings')} {findings.length}</TabsTrigger>
            </TabsList>
            <TabsContent value="assets"><AssetResultView model={model} anchorPrefix={id} /></TabsContent>
            <TabsContent value="findings"><FindingsPanel findings={findings} /></TabsContent>
          </Tabs>
        )}
        {loading && (
          <div className="flex items-center gap-2 px-3 py-2 text-xs text-muted-foreground">
            <Loader2 className="h-3 w-3 animate-spin" />
            <span>{tChat('toolCard.loadingResults')}</span>
          </div>
        )}
        {toolArgs && (
          <details className="border-t border-border">
            <summary className="cursor-pointer px-3 py-1.5 text-[10px] font-medium uppercase tracking-wider text-muted-foreground hover:text-foreground">
              {labels.arguments}
            </summary>
            <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words px-3 pb-2 font-mono text-xs text-muted-foreground">
              {formattedArgs}
            </pre>
          </details>
        )}
        {displayResult !== undefined && (
          <details className="border-t border-border" open={!nodes && !loading}>
            <summary className="cursor-pointer px-3 py-1.5 text-[10px] font-medium uppercase tracking-wider text-muted-foreground hover:text-foreground">
              {tChat('toolCard.rawOutput')}
            </summary>
            <pre className="max-h-60 overflow-auto whitespace-pre-wrap break-words px-3 pb-2 font-mono text-xs text-muted-foreground">
              {displayResult}
            </pre>
          </details>
        )}
      </div>
    </DisclosureCard>
  )
}
