import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, CircleX, Loader2, Wrench } from 'lucide-react'
import { EasmResultFromNodes, type SCONode } from '@cyber/cstx-easm'
import { Badge, DisclosureCard } from '@cyber/ui'
import { cn } from '@cyber/theme'
import { ToolCallDisplay, formatArgs, stripAnsiControl, summarizeArgs } from '@/viewer'
import { listSCONodes, subscribeCSTXChanges } from '../../lib/cstx-runtime'

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
  const [nodes, setNodes] = useState<SCONode[] | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!id || pending || error) {
      setNodes(null)
      return
    }
    let disposed = false
    const load = () => {
      setLoading(true)
      void listSCONodes({ scanId: id }).then((value) => {
        if (!disposed) setNodes(value.length > 0 ? value : null)
      }).catch(() => {
        if (!disposed) setNodes(null)
      }).finally(() => {
        if (!disposed) setLoading(false)
      })
    }
    const unsubscribe = subscribeCSTXChanges(load)
    load()
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

  if (!nodes || nodes.length === 0) {
    return (
      <ToolCallDisplay
        toolName={toolName}
        toolArgs={toolArgs}
        result={result}
        pending={pending}
        error={error}
        labels={labels}
      />
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
        {nodes && nodes.length > 0 && (
          <div className="p-3">
            <EasmResultFromNodes nodes={nodes} />
          </div>
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
